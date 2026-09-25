// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it } from 'vitest'

import EventDetail from './EventDetail.svelte'
import type { EventRecord, EventStatus } from '../lib/types'

afterEach(() => {
  cleanup()
})

const detailed: EventRecord = {
  event_id: 'evt-9',
  run_id: 'run-1',
  sequence: 9,
  source: 'claude-code',
  source_event_id: 'src-9',
  source_sequence: 77,
  source_version: '2.0.1',
  type: 'tool.completed',
  status: 'completed' as EventStatus,
  occurred_at: '2026-09-25T10:00:03.250Z',
  received_at: '2026-09-25T10:00:03.400Z',
  step_id: 'web/src/lib/api.ts',
  parent_step_id: 'session-root',
  capture: {
    mode: 'detailed',
    content_truncated: true,
    redacted_fields: 2,
    tool_name: 'edit',
  },
  attributes: { tool: 'edit', path: 'web/src/lib/api.ts', input_tokens: 1200 },
  content: { text: 'replaced the fetch signature' },
  searchable: true,
  raw: { tool_input: '[redacted]' },
}

const metadataOnly: EventRecord = {
  ...detailed,
  event_id: 'evt-10',
  capture: { mode: 'metadata', content_withheld: true, content_fields: ['prompt', 'text'] },
  attributes: {},
  content: null,
  searchable: false,
  raw: null,
}

describe('EventDetail', () => {
  it('shows timing, ordering keys, and source identity', () => {
    render(EventDetail, { event: detailed })

    expect(screen.getByText('Tool completed')).toBeTruthy()
    expect(screen.getByText('#77')).toBeTruthy()
    expect(screen.getByText('#9')).toBeTruthy()
    expect(screen.getByText('2.0.1')).toBeTruthy()
    expect(screen.getByText('src-9')).toBeTruthy()
    expect(screen.getByText('+150 ms')).toBeTruthy()
  })

  it('names the step and its parent', () => {
    render(EventDetail, { event: detailed })

    expect(screen.getByText('child of session-root')).toBeTruthy()
  })

  it('reports fields the source never sent instead of inventing values', () => {
    render(EventDetail, {
      event: {
        ...metadataOnly,
        source_sequence: undefined,
        source_version: '',
        step_id: null,
        parent_step_id: null,
      },
    })

    expect(screen.getAllByText('not reported').length).toBeGreaterThan(0)
    expect(screen.getByText('run level')).toBeTruthy()
    expect(screen.getByText('not indexed for search')).toBeTruthy()
  })

  it('names the effective capture mode in plain terms', () => {
    render(EventDetail, { event: detailed })

    expect(
      screen.getByText('detailed: prompt and tool bodies retained after redaction'),
    ).toBeTruthy()
  })

  it('flags an unrecognized capture mode instead of guessing its coverage', () => {
    render(EventDetail, { event: { ...metadataOnly, capture: { mode: 'experimental' } } })

    expect(screen.getByText('experimental (mode not recognized by this build)')).toBeTruthy()
  })

  it('states that a metadata-mode body was withheld and names the fields', () => {
    render(EventDetail, { event: metadataOnly })

    expect(screen.getByText(/Prompt and tool body content was withheld/)).toBeTruthy()
    expect(screen.getByText(/prompt, text/)).toBeTruthy()
  })

  it('reports which part of the event hit the ingestion limit', () => {
    render(EventDetail, { event: detailed })

    expect(screen.getByText('Truncated at the ingestion limit: content.')).toBeTruthy()
    expect(screen.getByText(/2 fields redacted before storage\./)).toBeTruthy()
  })

  it('says the raw payload was withheld rather than implying one exists', () => {
    render(EventDetail, {
      event: { ...metadataOnly, capture: { ...metadataOnly.capture, raw_withheld: true } },
    })

    expect(screen.getByText(/withheld: the active capture mode drops the raw body/)).toBeTruthy()
    expect(screen.queryByText(/no raw payload retained/)).toBeNull()
  })

  it('says no raw payload was sent when none was stored', () => {
    render(EventDetail, { event: { ...metadataOnly, capture: { mode: 'metadata' } } })

    expect(screen.getByText('no raw payload retained: the source sent none.')).toBeTruthy()
  })

  it('shows source-reported capture facts alongside the store facts', () => {
    render(EventDetail, { event: detailed })

    expect(screen.getByText('tool_name')).toBeTruthy()
  })

  it('renders a redacted raw payload as inert text, not markup', () => {
    const hostile: EventRecord = {
      ...detailed,
      raw: { note: '<img src=x onerror="globalThis.__pwned=1">' },
    }
    render(EventDetail, { event: hostile })

    expect(document.querySelector('img')).toBeNull()
    expect((globalThis as Record<string, unknown>).__pwned).toBeUndefined()
    expect(screen.getByText(/onerror/)).toBeTruthy()
  })

  it('omits the content block when no content was captured', () => {
    render(EventDetail, { event: metadataOnly })

    expect(screen.queryByText('Content')).toBeNull()
  })
})
