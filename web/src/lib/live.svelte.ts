import { connect, type ConnectOptions, type RealtimeConnection } from './realtime'
import type { ConnectionState, GapNotice, ServerMessage } from './types'

type Listener = (message: ServerMessage) => void

/**
 * Owns the live update socket for the whole dashboard.
 *
 * The socket only ever reports that something changed. It never mutates the run
 * index, and a sequence gap never advances the client's position: the pages
 * refetch the range the notice names and acknowledge it once applied, so a
 * missing event surfaces as a gap rather than a silently short timeline.
 */
class LiveFeed {
  connection = $state<ConnectionState>('idle')
  /** Gap notices the interface must still recover from, oldest first. */
  notices = $state<GapNotice[]>([])
  lastServerTime = $state<string | null>(null)
  lastError = $state<string | null>(null)

  #socket: RealtimeConnection | null = null
  #listeners = new Set<Listener>()

  /** Opens the socket once. Safe to call from more than one component. */
  start(options: ConnectOptions = {}): void {
    if (this.#socket !== null) return
    this.#socket = connect(
      {
        onMessage: (message) => {
          if (message.kind === 'ping' || message.kind === 'hello') {
            this.lastServerTime = message.server_time
          }
          for (const listener of this.#listeners) listener(message)
        },
        onGap: (notice) => {
          this.notices = [...this.notices, { ...notice }]
        },
        onState: (state) => {
          this.connection = state
        },
        onError: (error) => {
          this.lastError = error.message
        },
      },
      undefined,
      options,
    )
  }

  stop(): void {
    this.#socket?.close()
    this.#socket = null
    this.connection = 'idle'
  }

  watch(runId: string, afterSequence: number): void {
    this.#socket?.watch(runId, afterSequence)
  }

  acknowledge(runId: string, sequence: number): void {
    this.#socket?.acknowledge(runId, sequence)
  }

  /** Leaves `recovering` only after the named range has been refetched. */
  recovered(): void {
    this.#socket?.recovered()
  }

  forget(runId: string): void {
    this.#socket?.forget(runId)
  }

  /** Removes a notice once the page has refetched that exact range. */
  clearNotice(notice: { runId: string | null; afterSequence: number }): void {
    this.notices = this.notices.filter(
      (candidate) =>
        !(
          candidate.runId === notice.runId &&
          candidate.afterSequence === notice.afterSequence
        ),
    )
  }

  subscribe(listener: Listener): () => void {
    this.#listeners.add(listener)
    return () => this.#listeners.delete(listener)
  }

  /**
   * Adopts an already-built socket. `start` is a no-op afterwards, so a page test
   * can drive delivery without opening a real connection.
   */
  attach(connection: RealtimeConnection): void {
    this.#socket = connection
  }
}

export const live = new LiveFeed()
