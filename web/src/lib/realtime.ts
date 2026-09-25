import type { ConnectionState, GapNotice, ServerMessage } from './types'

// The live feed and the page store both reach for these through this module, so
// the connection contract has one home.
export type { ConnectionState, GapNotice, ServerMessage } from './types'

export type MessageHandler = (message: ServerMessage) => void
export type GapHandler = (notice: GapNotice) => void
export type StateHandler = (state: ConnectionState) => void
export type ErrorHandler = (error: Error) => void

export interface ConnectOptions {
  socketFactory?: (url: string) => WebSocket
  reconnectBaseMS?: number
  reconnectMaxMS?: number
  random?: () => number
}

export interface RealtimeConnection {
  close(): void
  watch(runId: string, afterSequence: number): void
  acknowledge(runId: string, sequence: number): void
  recovered(): void
  forget(runId: string): void
  state(): ConnectionState
  readonly tracker: SequenceTracker
}

export interface Handlers {
  onMessage: MessageHandler
  onGap: GapHandler
  onState?: StateHandler
  onError?: ErrorHandler
  onSchedule?: (delay: number) => void
}

/**
 * Tracks, per run, the highest contiguous ingest sequence the client holds. A
 * skipped sequence does not advance the cursor, so the gap keeps being reported
 * until the client acknowledges the range it actually refetched.
 */
export class SequenceTracker {
  readonly #cursors = new Map<string, number>()

  watch(runId: string, afterSequence: number): void {
    this.#cursors.set(runId, afterSequence)
  }

  /** Returns the last applied sequence when a gap was detected, else null. */
  observe(runId: string, sequence: number): number | null {
    const current = this.#cursors.get(runId)
    if (current === undefined) return null
    if (sequence === current + 1) {
      this.#cursors.set(runId, sequence)
      return null
    }
    // A gap must not move the cursor forward: those events are still missing.
    return current
  }

  acknowledge(runId: string, sequence: number): void {
    this.#cursors.set(runId, sequence)
  }

  forget(runId: string): void {
    this.#cursors.delete(runId)
  }

  lastAcknowledged(runId: string): number | null {
    const current = this.#cursors.get(runId)
    return current === undefined ? null : current
  }

  watchedRuns(): string[] {
    return [...this.#cursors.keys()]
  }
}

function decode(raw: string): ServerMessage | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null) return null
  const candidate = parsed as { kind?: unknown }
  if (typeof candidate.kind !== 'string') return null
  return parsed as ServerMessage
}

/**
 * Opens the live update stream. The socket only reports that something changed;
 * it never mutates a run, and a detected gap moves the connection into
 * `recovering` until the client confirms it has refetched the missing range.
 */
export function connect(
  handlers: MessageHandler | Handlers,
  onGap?: GapHandler,
  options: ConnectOptions = {},
): RealtimeConnection {
  const resolved: Handlers =
    typeof handlers === 'function'
      ? { onMessage: handlers, onGap: onGap ?? (() => {}) }
      : handlers

  const {
    socketFactory,
    reconnectBaseMS = 500,
    reconnectMaxMS = 30_000,
    random = Math.random,
  } = options

  const makeSocket = socketFactory ?? ((url: string) => new WebSocket(url))
  const tracker = new SequenceTracker()

  let socket: WebSocket | null = null
  let state: ConnectionState = 'idle'
  let attempt = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let closed = false
  // The first successful open is not a reconnect, so it must not claim a gap.
  let everOpened = false

  const setState = (next: ConnectionState) => {
    if (state === next) return
    state = next
    resolved.onState?.(next)
  }

  const url = (): string => {
    if (typeof window === 'undefined' || !window.location) return 'ws://127.0.0.1/api/v1/stream'
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${protocol}//${window.location.host}/api/v1/stream`
  }

  const send = (payload: unknown) => {
    if (socket && socket.readyState === 1) {
      try {
        socket.send(JSON.stringify(payload))
      } catch (error) {
        resolved.onError?.(error instanceof Error ? error : new Error(String(error)))
      }
    }
  }

  const reportGapsForReconnect = () => {
    resolved.onGap({ runId: null, afterSequence: 0, reason: 'reconnect' })
    for (const runId of tracker.watchedRuns()) {
      const after = tracker.lastAcknowledged(runId) ?? 0
      resolved.onGap({ runId, afterSequence: after, reason: 'reconnect' })
    }
  }

  const scheduleReconnect = () => {
    if (closed) return
    // Deterministic exponential backoff, capped. A symmetric jitter term keeps
    // several idle dashboards from reconnecting on the same tick.
    const exponential = Math.min(reconnectMaxMS, reconnectBaseMS * 2 ** attempt)
    const jitter = Math.round((random() - 0.5) * (reconnectBaseMS / 2))
    const delay = Math.max(reconnectBaseMS, exponential + jitter)
    resolved.onSchedule?.(delay)
    timer = setTimeout(open, delay)
    attempt += 1
  }

  function open(): void {
    if (closed) return
    setState('connecting')
    const current = makeSocket(url())
    socket = current

    current.onopen = () => {
      const isReconnect = everOpened
      everOpened = true
      attempt = 0
      setState('live')
      for (const runId of tracker.watchedRuns()) {
        send({ type: 'watch', run_id: runId, after_sequence: tracker.lastAcknowledged(runId) ?? 0 })
      }
      // A reconnect is itself a gap: anything may have been committed while the
      // client was away, so every watched range is re-requested before the
      // interface returns to claiming it is live.
      if (isReconnect) {
        reportGapsForReconnect()
        setState('recovering')
      }
    }

    current.onmessage = (event: { data: string }) => {
      const message = decode(String(event.data))
      if (!message) {
        resolved.onError?.(new Error('The live stream sent a frame this build cannot read'))
        return
      }
      if (message.kind === 'event.appended' && message.run_id) {
        const lastApplied = tracker.observe(message.run_id, message.sequence)
        if (lastApplied !== null) {
          resolved.onGap({ runId: message.run_id, afterSequence: lastApplied, reason: 'sequence' })
          setState('recovering')
        }
      }
      if (message.kind === 'run.deleted' && message.run_id) {
        tracker.forget(message.run_id)
      }
      resolved.onMessage(message)
    }

    current.onerror = () => {
      resolved.onError?.(new Error('The live stream reported an error'))
    }

    current.onclose = () => {
      socket = null
      if (closed) {
        setState('offline')
        return
      }
      setState('connecting')
      scheduleReconnect()
    }
  }

  open()

  return {
    tracker,
    close(): void {
      closed = true
      if (timer !== undefined) clearTimeout(timer)
      timer = undefined
      socket?.close()
      socket = null
      setState('offline')
    },
    watch(runId: string, afterSequence: number): void {
      tracker.watch(runId, afterSequence)
      send({ type: 'watch', run_id: runId, after_sequence: afterSequence })
    },
    acknowledge(runId: string, sequence: number): void {
      tracker.acknowledge(runId, sequence)
      send({ type: 'acknowledge', run_id: runId, sequence })
    },
    recovered(): void {
      send({ type: 'recovered' })
      setState('live')
    },
    forget(runId: string): void {
      tracker.forget(runId)
      send({ type: 'forget', run_id: runId })
    },
    state(): ConnectionState {
      return state
    },
  }
}
