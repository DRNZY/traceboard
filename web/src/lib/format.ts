import type { EventStatus, RunStatus } from './types'

export interface StatusPresentation {
  /** A word. Never a colour, never a shape on its own. */
  text: string
  /** A distinct glyph so status survives a monochrome or colour-blind reading. */
  shape: string
  tone: 'positive' | 'critical' | 'caution' | 'neutral'
}

const STATUS: Record<RunStatus, StatusPresentation> = {
  started: { text: 'Running', shape: '›', tone: 'caution' },
  completed: { text: 'Completed', shape: '✓', tone: 'positive' },
  failed: { text: 'Failed', shape: '✕', tone: 'critical' },
  cancelled: { text: 'Cancelled', shape: '⊘', tone: 'neutral' },
  incomplete: { text: 'Incomplete', shape: '◐', tone: 'caution' },
  unknown: { text: 'Unknown', shape: '?', tone: 'neutral' },
}

export function formatStatus(status: RunStatus | string): StatusPresentation {
  const known = STATUS[status as RunStatus]
  if (known) return { ...known }
  // An unrecognized status is reported verbatim rather than assumed benign.
  return { text: String(status), shape: '?', tone: 'neutral' }
}

const EVENT_TYPE_LABELS: Record<string, string> = {
  'run.started': 'Run started',
  'run.completed': 'Run completed',
  'run.failed': 'Run failed',
  'run.cancelled': 'Run cancelled',
  'run.incomplete': 'Run incomplete',
  'prompt.received': 'Prompt received',
  'model.requested': 'Model requested',
  'model.completed': 'Model completed',
  'tool.started': 'Tool started',
  'tool.completed': 'Tool completed',
  'tool.failed': 'Tool failed',
  'file.changed': 'File changed',
  'command.started': 'Command started',
  'command.completed': 'Command completed',
  'command.failed': 'Command failed',
  'permission.requested': 'Permission requested',
  'permission.resolved': 'Permission resolved',
  'subagent.started': 'Subagent started',
  'subagent.completed': 'Subagent completed',
  'context.compacted': 'Context compacted',
  'error.recorded': 'Error recorded',
  'source.connected': 'Source connected',
  'source.disconnected': 'Source disconnected',
  'source.extension': 'Source event',
}

/**
 * Labels a normalized event type. An event outside the contract is labelled with
 * its own name and marked as a source extension, so the interface never forces
 * an unknown event into a category it does not belong to.
 */
export function formatEventType(type: string, source: string): string {
  const known = EVENT_TYPE_LABELS[type]
  if (known) return known
  return `${type} (${source} extension)`
}

const EVENT_STATUS_LABELS: Record<EventStatus, string> = {
  started: 'Started',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
  incomplete: 'Incomplete',
  unknown: 'Unknown',
}

export function formatEventStatus(status: EventStatus | string): string {
  return EVENT_STATUS_LABELS[status as EventStatus] ?? String(status)
}

export function formatDuration(milliseconds: number | null | undefined): string {
  if (milliseconds === null || milliseconds === undefined) return 'not reported'
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return 'not reported'
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(1)} s`
  if (milliseconds < 3_600_000) {
    // Truncate rather than round, so 59:59.999 is reported as 59 m 59 s.
    const totalSeconds = Math.floor(milliseconds / 1000)
    return `${Math.floor(totalSeconds / 60)} m ${String(totalSeconds % 60).padStart(2, '0')} s`
  }
  const totalMinutes = Math.floor(milliseconds / 60_000)
  return `${Math.floor(totalMinutes / 60)} h ${String(totalMinutes % 60).padStart(2, '0')} m`
}

const NOT_REPORTED = 'not reported'

export function formatTimestamp(value: string | null | undefined): string {
  if (!value) return NOT_REPORTED
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return 'unparsable timestamp'
  return `${utcDate(parsed)} ${utcTime(parsed, true)} UTC`
}

/** `2026-09-25` from a timestamp already normalised to UTC. */
function utcDate(parsed: Date): string {
  return parsed.toISOString().slice(0, 10)
}

/** `19:51:04` or `19:51:04.123`; a zero fraction is never printed as `.000`. */
function utcTime(parsed: Date, withMillis: boolean): string {
  const iso = parsed.toISOString()
  const fraction = iso.slice(19, 23)
  const base = iso.slice(11, 19)
  if (!withMillis || fraction === '.000') return base
  return `${base}${fraction}`
}

/** Time of day only, for dense timeline rows where the date is in the header. */
export function formatOffsetTime(value: string | null | undefined): string {
  if (!value) return '--:--:--'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return '--:--:--'
  return utcTime(parsed, true)
}

/** Signed gap between two events, or an explicit refusal to invent one. */
export function formatElapsed(
  from: string | null | undefined,
  to: string | null | undefined,
): string {
  if (!from || !to) return 'unknown gap'
  const start = new Date(from).getTime()
  const end = new Date(to).getTime()
  if (Number.isNaN(start) || Number.isNaN(end)) return 'unknown gap'
  const delta = end - start
  if (delta < 0) return 'out of order'
  if (delta === 0) return '+0 ms'
  if (delta < 1000) return `+${Math.round(delta)} ms`
  if (delta < 60_000) return `+${(delta / 1000).toFixed(1)} s`
  return `+${Math.floor(delta / 60_000)} m ${String(Math.round((delta % 60_000) / 1000)).padStart(2, '0')} s`
}

export function formatTokens(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return NOT_REPORTED
  return new Intl.NumberFormat('en-US').format(value)
}

export function formatCount(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return NOT_REPORTED
  return new Intl.NumberFormat('en-US').format(value)
}

export function formatNumber(
  value: number | null | undefined,
  zero: string,
  missing: string = NOT_REPORTED,
): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return missing
  return value === 0 ? zero : new Intl.NumberFormat('en-US').format(value)
}

/** Keeps the tail of a long path, which is the part that identifies it. */
export function formatFilePath(value: string | null | undefined): string {
  if (!value) return NOT_REPORTED
  const segments = value.split('/')
  if (segments.length <= 4) return value
  return `.../${segments.slice(-4).join('/')}`
}

export function truncateText(value: string, limit: number): string {
  if (value.length <= limit) return value
  return `${value.slice(0, limit)}[truncated]`
}

export function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KiB`
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`
}

export function formatAge(from: string | null | undefined, now: string): string {
  if (!from) return NOT_REPORTED
  const start = new Date(from).getTime()
  const reference = new Date(now).getTime()
  if (Number.isNaN(start) || Number.isNaN(reference)) return NOT_REPORTED
  return `+${((reference - start) / 1000).toFixed(1)} s`
}
