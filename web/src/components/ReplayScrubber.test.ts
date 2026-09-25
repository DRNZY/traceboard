// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ReplayScrubber, { reconstructRunState } from './ReplayScrubber.svelte'
import type { EventRecord } from '../lib/types'

function makeEvent(overrides: Partial<EventRecord> & { sequence: number; type: string }): EventRecord {
  return {
    event_id: `evt-${overrides.sequence}`,
    source_event_id: `src-${overrides.sequence}`,
    source: 'opencode',
    source_version: '1.0.0',
    run_id: 'run-1',
    occurred_at: `2026-09-25T10:00:${String(overrides.sequence).padStart(2, '0')}Z`,
    received_at: `2026-09-25T10:00:${String(overrides.sequence).padStart(2, '0')}Z`,
    status: 'completed',
    capture: {},
    attributes: {},
    searchable: false,
    ...overrides,
  }
}

const events: EventRecord[] = [
  makeEvent({
    sequence: 1,
    type: 'run.started',
    attributes: { project: 'traceboard' },
  }),
  makeEvent({ sequence: 2, type: 'prompt.received', content: { text: 'fix the parser' } }),
  makeEvent({ sequence: 3, type: 'model.completed', attributes: { model: 'claude-opus-5', input_tokens: 900, output_tokens: 120 } }),
  makeEvent({
    sequence: 4,
    type: 'tool.started',
    step_id: 'step-1',
    attributes: { tool: 'edit', path: 'web/src/lib/api.ts' },
  }),
  makeEvent({
    sequence: 5,
    type: 'file.changed',
    step_id: 'step-1',
    attributes: { path: 'web/src/lib/api.ts', operation: 'modified' },
  }),
  makeEvent({
    sequence: 6,
    type: 'tool.failed',
    status: 'failed',
    step_id: 'step-2',
    parent_step_id: 'step-1',
    attributes: { tool: 'bash', error: 'exit status 1' },
  }),
]

afterEach(() => {
  cleanup()
})

describe('reconstructRunState', () => {
  it('starts empty before any event is included', () => {
    const state = reconstructRunState(events, -1)

    expect(state.index).toBe(-1)
    expect(state.eventCount).toBe(events.length)
    expect(state.filesChanged).toEqual([])
    expect(state.toolsInvoked).toEqual([])
    expect(state.modelsUsed).toEqual([])
    expect(state.tokens.input).toBeNull()
    expect(state.tokens.output).toBeNull()
    expect(state.currentStepId).toBeNull()
  })

  it('includes the selected event and everything before it', () => {
    const state = reconstructRunState(events, 2)

    // Index 2 is the third recorded event, matching the 0-based slider.
    expect(state.appliedEvents.map((event) => event.sequence)).toEqual([1, 2, 3])
    expect(state.index).toBe(2)
    // The third event is the model call, and nothing after it is counted.
    expect(state.filesChanged).toEqual([])
    expect(state.modelsUsed).toEqual(['claude-opus-5'])
    expect(state.tokens).toEqual({ input: 900, output: 120 })
  })

  it('never folds in an event after the selected index', () => {
    const partial = reconstructRunState(events, 3)
    const full = reconstructRunState(events, events.length - 1)

    expect(partial.appliedEvents.map((event) => event.sequence)).toEqual([1, 2, 3, 4])
    expect(partial.toolsInvoked).toEqual(['edit'])
    expect(partial.firstFailure).toBeNull()
    expect(partial.tokens).toEqual({ input: 900, output: 120 })

    // The fifth and sixth events are present in the full fold only.
    expect(full.toolsInvoked).toEqual(['edit', 'bash'])
    expect(full.firstFailure?.event_id).toBe('evt-6')
  })

  it('accumulates file changes, tools, and models deterministically', () => {
    const state = reconstructRunState(events, events.length - 1)

    expect(state.filesChanged).toEqual(['web/src/lib/api.ts'])
    expect(state.toolsInvoked).toEqual(['edit', 'bash'])
    expect(state.modelsUsed).toEqual(['claude-opus-5'])
    expect(state.tokens).toEqual({ input: 900, output: 120 })
    expect(state.currentEvent?.type).toBe('tool.failed')
    expect(state.currentStepId).toBe('step-2')
    expect(state.parentStepId).toBe('step-1')
  })

  it('names the first source-reported failure and keeps counting after it', () => {
    const state = reconstructRunState(events, events.length - 1)

    expect(state.firstFailure?.event_id).toBe('evt-6')
    expect(state.stepCount).toBe(2)
  })

  it('reports an empty run honestly', () => {
    const state = reconstructRunState([], 0)

    expect(state.eventCount).toBe(0)
    expect(state.appliedEvents).toEqual([])
    expect(state.currentEvent).toBeNull()
  })
})

describe('ReplayScrubber', () => {
  function renderScrubber(overrides: Record<string, unknown> = {}) {
    const props = {
      runEvents: events,
      selectedIndex: events.length - 1,
      incomplete: false,
      onIndexChange: vi.fn(),
      ...overrides,
    }
    return { props, ...render(ReplayScrubber, props) }
  }

  it('offers no way to re-execute a captured action', () => {
    const { container } = renderScrubber()

    const labels = [...container.querySelectorAll('button')]
      .map((button) => (button.textContent ?? '').trim().toLowerCase())
      .join(' | ')
    expect(labels).not.toMatch(/execute|re-?run|replay|apply|resume|retry/)
    expect(container.querySelector('form')).toBeNull()
  })

  it('starts at the last recorded event', () => {
    renderScrubber()

    const slider = screen.getByLabelText('Replay position') as HTMLInputElement
    expect(slider.value).toBe('5')
    expect(slider.max).toBe('5')
  })

  it('reports an integer position, never a fraction of an event', async () => {
    const { props } = renderScrubber()

    await fireEvent.input(screen.getByLabelText('Replay position'), { target: { value: '2.7' } })

    expect(props.onIndexChange).toHaveBeenCalledWith(2)
  })

  it('steps forward and backward one event at a time', async () => {
    const { props } = renderScrubber({ selectedIndex: 2 })

    await fireEvent.click(screen.getByRole('button', { name: 'Next event' }))
    expect(props.onIndexChange).toHaveBeenLastCalledWith(3)

    await fireEvent.click(screen.getByRole('button', { name: 'Previous event' }))
    expect(props.onIndexChange).toHaveBeenLastCalledWith(1)
  })

  it('cannot step before the first event or past the last one', () => {
    const { rerender } = renderScrubber({ selectedIndex: 0 })

    expect((screen.getByRole('button', { name: 'Previous event' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Next event' }) as HTMLButtonElement).disabled).toBe(false)
    rerender({})
  })

  it('shows the reconstructed state at the selected event', () => {
    renderScrubber({ selectedIndex: 4 })

    const state = screen.getByTestId('replay-state')
    expect(state.textContent).toContain('web/src/lib/api.ts')
    expect(state.textContent).toContain('edit')
    expect(state.textContent).toContain('900')
    expect(state.textContent).not.toContain('bash')
  })

  it('says the run is incomplete instead of implying a full replay', () => {
    renderScrubber({ incomplete: true })

    expect(screen.getByRole('status').textContent).toMatch(/incomplete|missing/i)
  })

  it('explains that replay only reconstructs recorded data', () => {
    renderScrubber()

    expect(screen.getByRole('note').textContent).toMatch(/recorded data|no action is run/i)
  })
})
