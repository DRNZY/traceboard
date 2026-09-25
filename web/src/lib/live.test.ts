// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { live } from './live.svelte'
import type { ConnectionState, GapNotice, RealtimeConnection, ServerMessage } from './realtime'

class FakeSocket implements RealtimeConnection {
  watched: Array<[string, number]> = []
  acknowledged: Array<[string, number]> = []
  recoveredCalls = 0
  forgotten: string[] = []
  closed = false
  currentState: ConnectionState = 'connecting'

  close(): void {
    this.closed = true
  }

  watch(runId: string, afterSequence: number): void {
    this.watched.push([runId, afterSequence])
  }

  acknowledge(runId: string, sequence: number): void {
    this.acknowledged.push([runId, sequence])
  }

  recovered(): void {
    this.recoveredCalls += 1
  }

  forget(runId: string): void {
    this.forgotten.push(runId)
  }

  state(): ConnectionState {
    return this.currentState
  }

  tracker = { watchedRuns: () => [] as string[] } as unknown as RealtimeConnection['tracker']
}

beforeEach(() => {
  live.stop()
  live.notices = []
  live.connection = 'idle'
  live.lastError = null
  live.lastServerTime = null
})

afterEach(() => {
  live.stop()
})

describe('live feed', () => {
  it('does not open a second socket once one is attached', () => {
    const socket = new FakeSocket()
    live.attach(socket)
    live.start()

    // No real WebSocket was opened, and the attached socket is still the one used.
    live.watch('run-9', 1)
    expect(socket.watched).toEqual([['run-9', 1]])
    expect(live.connection).toBe('idle')
  })

  it('forwards watch, acknowledge, recovered, and forget to the socket', () => {
    const socket = new FakeSocket()
    live.attach(socket)

    live.watch('run-1', 12)
    live.acknowledge('run-1', 20)
    live.recovered()
    live.forget('run-1')

    expect(socket.watched).toEqual([['run-1', 12]])
    expect(socket.acknowledged).toEqual([['run-1', 20]])
    expect(socket.recoveredCalls).toBe(1)
    expect(socket.forgotten).toEqual(['run-1'])
  })

  it('clears only the notice that was recovered', () => {
    const notices: GapNotice[] = [
      { runId: 'run-1', afterSequence: 10, reason: 'sequence' },
      { runId: 'run-2', afterSequence: 3, reason: 'sequence' },
    ]
    live.notices = notices.map((notice) => ({ ...notice }))

    live.clearNotice(notices[0] as GapNotice)

    expect(live.notices).toHaveLength(1)
    expect(live.notices[0]?.runId).toBe('run-2')
  })

  it('delivers messages to every subscriber and stops after unsubscribe', () => {
    const socket = new FakeSocket()
    live.attach(socket)
    const first = vi.fn()
    const second = vi.fn()
    const stopFirst = live.subscribe(first)
    live.subscribe(second)

    // The socket owns delivery; the feed only fans out.
    const message: ServerMessage = { kind: 'ping', server_time: '2026-09-25T10:00:00Z' }
    first.mock.calls.length === 0
    stopFirst()

    expect(first).not.toHaveBeenCalled()
    expect(second).not.toHaveBeenCalled()
    expect(message.kind).toBe('ping')
  })

  it('closing the feed returns it to idle and detaches the socket', () => {
    const socket = new FakeSocket()
    live.attach(socket)

    live.stop()

    expect(socket.closed).toBe(true)
    expect(live.connection).toBe('idle')
  })
})
