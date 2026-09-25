export type CaptureMode = 'off' | 'metadata' | 'detailed'

export type RunStatus =
  | 'started'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'incomplete'
  | 'unknown'

export type EventStatus = RunStatus

export type AlertState = 'open' | 'acknowledged' | 'resolved'

export interface Run {
  id: string
  source: string
  project_id?: string | null
  project_name?: string | null
  parent_run_id?: string | null
  title?: string | null
  status: RunStatus
  started_at?: string | null
  ended_at?: string | null
  last_event_at?: string | null
  duration_ms?: number | null
  event_count: number
  error_count: number
  next_sequence: number
  capture_modes: CaptureMode[]
  models: string[]
  input_tokens?: number | null
  output_tokens?: number | null
  open_alerts: number
  subagent_runs: number
  created_at: string
  updated_at: string
}

export interface RunPage {
  runs: Run[]
  next_cursor: string | null
}

/**
 * A filter is a set of search terms, not a validated record. The well-known
 * discriminators keep editor completion while still accepting any string, so a
 * filter built at runtime is not forced through a cast. The server validates
 * and rejects an unknown value.
 */
type Loose<T extends string> = T | (string & Record<never, never>)

export interface RunFilter {
  query?: string
  source?: string
  project_id?: string
  status?: Loose<RunStatus>
  capture_mode?: Loose<CaptureMode>
  alert_state?: Loose<'open' | 'none'>
  started_after?: string
  started_before?: string
  parent_run_id?: string
  subagent_only?: boolean
}

export interface Step {
  run_id: string
  id: string
  parent_step_id?: string | null
  type: string
  status: EventStatus
  started_at?: string | null
  ended_at?: string | null
  event_count: number
}

export interface EventRecord {
  event_id: string
  source_event_id: string
  source: string
  source_version: string
  run_id: string
  step_id?: string | null
  parent_step_id?: string | null
  source_sequence?: number | null
  sequence: number
  occurred_at: string
  received_at: string
  type: string
  status: EventStatus
  capture: Record<string, unknown>
  attributes: Record<string, unknown>
  content?: Record<string, unknown> | null
  raw?: unknown
  searchable: boolean
}

export interface EventPage {
  events: EventRecord[]
  next_sequence: number
  run_sequence: number
  has_more: boolean
  run_status: RunStatus
}

export interface RunDetail {
  run: Run
  steps: Step[]
  subagent_runs: Run[]
  alerts: Alert[]
}

export interface Alert {
  id: string
  type: string
  run_id?: string | null
  source?: string | null
  message: string
  created_at: string
  acknowledged_at?: string | null
  resolved_at?: string | null
  state: AlertState
}

export interface AlertFilter {
  state?: AlertState | ''
  run_id?: string
  limit?: number
}

export interface Source {
  name: string
  source_version: string
  capture_mode: CaptureMode
  connected: boolean
  last_heartbeat_at?: string | null
  run_count: number
  quarantine_count: number
  created_at: string
  updated_at: string
}

export interface Project {
  id: string
  name?: string | null
  path?: string | null
  run_count: number
  created_at: string
  updated_at: string
}

export interface QuarantineEntry {
  id: number
  source?: string | null
  source_event_id?: string | null
  payload: string
  reason: string
  created_at: string
}

export interface DeleteResult {
  run_id: string
  events: number
  steps: number
  alerts: number
  search_entries: number
}

export type ExportFormat = 'json' | 'markdown' | 'raw'

export interface ExportResult {
  path: string
  format: ExportFormat
  bytes: number
}

export interface Settings {
  version: string
  commit: string
  listen_address: string
  retention_days: number
  keep_newest_per_project: number
  database_path: string
  config_path: string
  sessions_valid: number
  ingest_token_configured: boolean
  dashboard_token_configured: boolean
}

export type ConnectionState = 'idle' | 'connecting' | 'live' | 'recovering' | 'offline'

export interface GapNotice {
  runId: string | null
  afterSequence: number
  reason: 'sequence' | 'reconnect'
}

export type ServerMessage =
  | { kind: 'hello'; server_time: string; stream_id: number }
  | { kind: 'ping'; server_time: string }
  | { kind: 'run.updated'; run: Run }
  | { kind: 'run.deleted'; run_id: string }
  | { kind: 'event.appended'; run_id: string; sequence: number; run_sequence: number }
  | { kind: 'alert.updated'; alert: Alert }
  | { kind: 'source.updated'; source: string }
