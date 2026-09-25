import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { connect, SequenceTracker, type MessageHandler, type GapHandler } from './realtime'
import type { ConnectionState, GapNotice, ServerMessage } from './types'

class FakeSocket {
  static instances: FakeSocket[] = []

  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  closeCalls = 0

  constructor(readonly url: string) {
    FakeSocket.instances.push(this)
  }

  close(): void {
    this.closeCalls += 1
  }

  open(): void {
    this.onopen?.()
  }

  deliver(message: ServerMessage): void {
    this.onmessage?.({ data: JSON.stringify(message) })
  }

  drop(): void {
    this.onclose?.()
  }
}

function setup() {
  FakeSocket.instances = []
  const messages: ServerMessage[] = []
  const gaps: GapNotice[] = []
  const states: ConnectionState[] = []
  const onMessage: MessageHandler = (message) => messages.push(message)
  const onGap: GapHandler = (notice) => gaps.push(notice)
  const socket = (): FakeSocket => FakeSocket.instances[FakeSocket.instances.length - 1]
  return { messages, gaps, states, onMessage, onGap, socket }
}

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('SequenceTracker', () => {
  it('accepts contiguous sequences without a gap', () => {
    const tracker = new SequenceTracker()
    tracker.watch('run-1', 4)

    expect(tracker.observe('run-1', 5)).toBeNull()
    expect(tracker.observe('run-1', 6)).toBeNull()
    expect(tracker.lastAcknowledged('run-1')).toBe(6)
  })

  it('reports the last applied sequence when a sequence is skipped', () => {
    const tracker = new SequenceTracker()
    tracker.watch('run-1', 4)

    expect(tracker.observe('run-1', 7)).toBe(4)
  })

  it('stays quiet for a run nobody is watching', () => {
    const tracker = new SequenceTracker()

    expect(tracker.observe('run-9', 900)).toBeNull()
    expect(tracker.lastAcknowledged('run-9')).toBeNull()
  })

  it('keeps reporting the gap until the missing range is acknowledged', () => {
    const tracker = new SequenceTracker()
    tracker.watch('run-1', 4)

    // A skip must not advance the cursor: those events are still missing.
    expect(tracker.observe('run-1', 7)).toBe(4)
    expect(tracker.observe('run-1', 8)).toBe(4)

    tracker.acknowledge('run-1', 7)
    expect(tracker.observe('run-1', 8)).toBeNull()
    expect(tracker.lastAcknowledged('run-1')).toBe(8)
  })

  it('forgets a deleted run', () => {
    const tracker = new SequenceTracker()
    tracker.watch('run-1', 4)
    tracker.forget('run-1')

    expect(tracker.lastAcknowledged('run-1')).toBeNull()
  })
})

describe('connect', () => {
  it('opens a same-origin socket and reports live state', () => {
    const { states, socket } = setup()
    const connection = connect({ onMessage: () => {}, onGap: () => {}, onState: (s) => states.push(s) }, undefined, {
      socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket,
    })

    socket().open()

    expect(states).toEqual(['connecting', 'live'])
    expect(socket().url.startsWith('ws://')).toBe(true)
    expect(socket().url).toContain('/api/v1/stream')
    connection.close()
  })

  it('delivers decoded server messages', () => {
    const { messages, socket } = setup()
    const connection = connect(
      { onMessage: (message) => messages.push(message), onGap: () => {} },
      undefined,
      { socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket },
    )
    socket().open()
    socket().deliver({ kind: 'run.updated', run: { id: 'run-1' } as never })

    expect(messages).toHaveLength(1)
    expect(messages[0].kind).toBe('run.updated')
    connection.close()
  })

  it('ignores malformed frames instead of breaking the stream', () => {
    const { socket } = setup()
    const errors: unknown[] = []
    const connection = connect(
      { onMessage: () => {}, onGap: () => {}, onError: (error) => errors.push(error) },
      undefined,
      { socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket },
    )
    socket().open()
    socket().onmessage?.({ data: 'not json' })
    socket().onmessage?.({ data: '{"no":"kind"}' })
    socket().deliver({ kind: 'ping', server_time: '2026-09-25T19:51:04Z' })

    expect(errors).toHaveLength(2)
    connection.close()
  })

  it('detects a sequence gap for the watched run and pauses live claims', () => {
    const { gaps, socket, states } = setup()
    const connection = connect(
      { onMessage: () => {}, onGap: (notice) => gaps.push(notice), onState: (state) => states.push(state) },
      undefined,
      { socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket },
    )
    socket().open()
    connection.watch('run-1', 4)
    socket().deliver({ kind: 'event.appended', run_id: 'run-1', sequence: 9, run_sequence: 10 })

    expect(gaps).toEqual([{ runId: 'run-1', afterSequence: 4, reason: 'sequence' }])
    expect(connection.state()).toBe('recovering')
    connection.close()
  })

  it('returns to live only after the client confirms recovery', () => {
    const { socket } = setup()
    const connection = connect(
      { onMessage: () => {}, onGap: () => {} },
      undefined,
      { socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket },
    )
    socket().open()
    connection.watch('run-1', 4)
    socket().deliver({ kind: 'event.appended', run_id: 'run-1', sequence: 9, run_sequence: 10 })

    expect(connection.state()).toBe('recovering')
    connection.recovered()
    expect(connection.state()).toBe('live')
    connection.close()
  })

  it('reconnects with backoff and asks for a snapshot', () => {
    const { gaps, socket } = setup()
    const connection = connect(
      { onMessage: () => {}, onGap: (notice) => gaps.push(notice) },
      undefined,
      {
        socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket,
        reconnectBaseMS: 1000,
        reconnectMaxMS: 10_000,
        random: () => 0.5,
      },
    )
    socket().open()
    connection.watch('run-1', 6)
    socket().drop()

    expect(connection.state()).toBe('connecting')
    expect(FakeSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(1000)
    expect(FakeSocket.instances).toHaveLength(2)

    socket().open()
    expect(gaps).toContainEqual({ runId: null, afterSequence: 0, reason: 'reconnect' })
    expect(gaps).toContainEqual({ runId: 'run-1', afterSequence: 6, reason: 'reconnect' })
    expect(connection.state()).toBe('recovering')
    connection.close()
  })

  it('backs off further on repeated failures and caps the delay', () => {
    const { socket } = setup()
    const delays: number[] = []
    const connection = connect(
      {
        onMessage: () => {},
        onGap: () => {},
        onSchedule: (delay) => delays.push(delay),
      },
      undefined,
      {
        socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket,
        reconnectBaseMS: 1000,
        reconnectMaxMS: 4000,
        random: () => 0.5,
      },
    )
    socket().drop()
    vi.advanceTimersByTime(1000)
    socket().drop()
    vi.advanceTimersByTime(2000)
    socket().drop()

    expect(delays).toEqual([1000, 2000, 4000])
    connection.close()
  })

  it('stops reconnecting after close', () => {
    const { socket } = setup()
    const connection = connect(
      { onMessage: () => {}, onGap: () => {} },
      undefined,
      {
        socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket,
        reconnectBaseMS: 1000,
        random: () => 0.5,
      },
    )
    socket().open()
    connection.close()
    vi.advanceTimersByTime(60_000)

    expect(FakeSocket.instances).toHaveLength(1)
    expect(connection.state()).toBe('offline')
  })

  it('forgets a deleted run so later events do not fake a gap', () => {
    const { gaps, socket } = setup()
    const connection = connect(
      { onMessage: () => {}, onGap: (notice) => gaps.push(notice) },
      undefined,
      { socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket },
    )
    socket().open()
    connection.watch('run-1', 4)
    socket().deliver({ kind: 'run.deleted', run_id: 'run-1' })
    socket().deliver({ kind: 'event.appended', run_id: 'run-1', sequence: 40, run_sequence: 41 })

    expect(gaps).toHaveLength(0)
    connection.close()
  })

  it('accepts the shorthand handler signature', () => {
    const { messages, socket } = setup()
    const connection = connect((message) => messages.push(message), () => {}, {
      socketFactory: (url) => new FakeSocket(url) as unknown as WebSocket,
    })
    socket().open()
    socket().deliver({ kind: 'ping', server_time: '2026-09-25T19:51:04Z' })

    expect(messages).toHaveLength(1)
    connection.close()
  })
})
