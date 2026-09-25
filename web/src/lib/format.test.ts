import { describe, expect, it } from 'vitest'

import {
  formatCount,
  formatDuration,
  formatElapsed,
  formatEventType,
  formatFilePath,
  formatNumber,
  formatOffsetTime,
  formatStatus,
  formatTimestamp,
  formatTokens,
  truncateText,
  type StatusPresentation,
} from './format'

describe('formatDuration', () => {
  it('scales units without floating point drift', () => {
    expect(formatDuration(0)).toBe('0 ms')
    expect(formatDuration(1)).toBe('1 ms')
    expect(formatDuration(999)).toBe('999 ms')
    expect(formatDuration(1000)).toBe('1.0 s')
    expect(formatDuration(1409)).toBe('1.4 s')
    expect(formatDuration(59_999)).toBe('60.0 s')
    expect(formatDuration(60_000)).toBe('1 m 00 s')
    expect(formatDuration(90_000)).toBe('1 m 30 s')
    expect(formatDuration(3_599_999)).toBe('59 m 59 s')
    expect(formatDuration(3_600_000)).toBe('1 h 00 m')
    expect(formatDuration(7_260_000)).toBe('2 h 01 m')
  })

  it('reports missing durations instead of inventing zero', () => {
    expect(formatDuration(null)).toBe('not reported')
    expect(formatDuration(undefined)).toBe('not reported')
    expect(formatDuration(Number.NaN)).toBe('not reported')
    expect(formatDuration(-5)).toBe('not reported')
  })

  it('is stable for the same input', () => {
    expect(formatDuration(123_456)).toBe(formatDuration(123_456))
  })
})

describe('formatTimestamp', () => {
  it('renders UTC without locale drift', () => {
    expect(formatTimestamp('2026-09-25T19:51:04.123Z')).toBe('2026-09-25 19:51:04.123 UTC')
    expect(formatTimestamp('2026-09-25T19:51:04Z')).toBe('2026-09-25 19:51:04 UTC')
    expect(formatTimestamp('2026-09-25T19:51:04+02:00')).toBe('2026-09-25 17:51:04 UTC')
  })

  it('labels unusable values', () => {
    expect(formatTimestamp(null)).toBe('not reported')
    expect(formatTimestamp(undefined)).toBe('not reported')
    expect(formatTimestamp('')).toBe('not reported')
    expect(formatTimestamp('not-a-date')).toBe('unparsable timestamp')
  })
})

describe('formatOffsetTime', () => {
  it('renders a stable time-of-day for timeline rows', () => {
    expect(formatOffsetTime('2026-09-25T19:51:04.123Z')).toBe('19:51:04.123')
    expect(formatOffsetTime('2026-09-25T19:51:04Z')).toBe('19:51:04')
    expect(formatOffsetTime('not-a-date')).toBe('--:--:--')
  })
})

describe('formatElapsed', () => {
  it('measures the gap between two events', () => {
    expect(formatElapsed('2026-09-25T19:51:04.000Z', '2026-09-25T19:51:06.500Z')).toBe('+2.5 s')
    expect(formatElapsed('2026-09-25T19:51:04.000Z', '2026-09-25T19:51:04.000Z')).toBe('+0 ms')
  })

  it('refuses to report a negative or unknown gap', () => {
    expect(formatElapsed('2026-09-25T19:51:06.000Z', '2026-09-25T19:51:04.000Z')).toBe('out of order')
    expect(formatElapsed(undefined, '2026-09-25T19:51:04.000Z')).toBe('unknown gap')
  })
})

describe('formatTokens', () => {
  it('groups thousands deterministically', () => {
    expect(formatTokens(0)).toBe('0')
    expect(formatTokens(999)).toBe('999')
    expect(formatTokens(1000)).toBe('1,000')
    expect(formatTokens(1_234_567)).toBe('1,234,567')
  })

  it('names the gap when a source reports no usage', () => {
    expect(formatTokens(null)).toBe('not reported')
    expect(formatTokens(undefined)).toBe('not reported')
    expect(formatTokens(-1)).toBe('not reported')
  })
})

describe('formatCount and formatNumber', () => {
  it('groups large counts', () => {
    expect(formatCount(0)).toBe('0')
    expect(formatCount(1234)).toBe('1,234')
    expect(formatCount(10_000)).toBe('10,000')
  })

  it('accepts a custom label for zero and missing values', () => {
    expect(formatNumber(0, 'none')).toBe('none')
    expect(formatNumber(undefined, 'not reported')).toBe('not reported')
    expect(formatNumber(0, 'none', 'not reported')).toBe('none')
    expect(formatNumber(null, 'none', 'not reported')).toBe('not reported')
  })
})

describe('formatEventType', () => {
  it('labels normalized event types', () => {
    expect(formatEventType('tool.completed', 'opencode')).toBe('Tool completed')
    expect(formatEventType('model.requested', 'claude-code')).toBe('Model requested')
    expect(formatEventType('file.changed', 'codex')).toBe('File changed')
  })

  it('marks extension events with their source instead of guessing a category', () => {
    const label = formatEventType('session.titled', 'opencode')
    expect(label).toBe('session.titled (opencode extension)')
    expect(label).not.toMatch(/tool|error/i)
  })
})

describe('formatStatus', () => {
  it('gives every status a text label and a distinct shape', () => {
    const statuses = ['started', 'completed', 'failed', 'cancelled', 'incomplete', 'unknown'] as const
    const rendered = statuses.map((status) => formatStatus(status))
    for (const presentation of rendered) {
      expect(presentation.text.length).toBeGreaterThan(0)
      expect(presentation.shape.length).toBeGreaterThan(0)
    }
    const shapes = new Set(rendered.map((presentation) => presentation.shape))
    expect(shapes.size).toBe(statuses.length)
    const texts = new Set(rendered.map((presentation) => presentation.text))
    expect(texts.size).toBe(statuses.length)
  })

  it('uses the same presentation for the same status', () => {
    expect(formatStatus('failed')).toEqual(formatStatus('failed'))
  })

  it('falls back for a status the dashboard does not know', () => {
    const unknown = formatStatus('quiesced' as never)
    expect(unknown.text).toBe('quiesced')
    expect(unknown.tone).toBe('neutral')
  })

  it('never encodes meaning in colour alone', () => {
    const failed: StatusPresentation = formatStatus('failed')
    expect(failed.text).toBe('Failed')
    expect(failed.shape).not.toBe(formatStatus('completed').shape)
  })
})

describe('formatFilePath', () => {
  it('keeps the tail of a long path readable', () => {
    expect(formatFilePath('src/lib/realtime.ts')).toBe('src/lib/realtime.ts')
    expect(formatFilePath('/home/darnell/Projects/traceboard/web/src/lib/api.ts')).toBe('.../web/src/lib/api.ts')
  })

  it('reports a missing path', () => {
    expect(formatFilePath('')).toBe('not reported')
    expect(formatFilePath(undefined)).toBe('not reported')
  })
})

describe('truncateText', () => {
  it('marks truncation explicitly', () => {
    expect(truncateText('short', 10)).toBe('short')
    const truncated = truncateText('abcdefghijkl', 8)
    expect(truncated.startsWith('abcdefgh')).toBe(true)
    expect(truncated.endsWith('[truncated]')).toBe(true)
  })

  it('never grows the string when the limit is tiny', () => {
    const result = truncateText('abcdefghij', 4)
    expect(result.length).toBeLessThanOrEqual(4 + '[truncated]'.length)
  })
})
