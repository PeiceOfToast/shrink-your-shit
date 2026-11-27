package events

import (
	"fmt"
	"time"
)

// Level is how loud an event is. Debug is hidden unless the bus is opened with Debug.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// rank orders levels for filters. An unknown level ranks below every known one.
func (l Level) rank() int {
	switch l {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	}
	return -1
}

// Kind is what sort of event it is. The UI picks its layout from this.
type Kind string

const (
	KindRunStart  Kind = "run_start"
	KindRunEnd    Kind = "run_end"
	KindStepStart Kind = "step_start"
	KindStepEnd   Kind = "step_end"
	KindDecision  Kind = "decision"
	KindCommand   Kind = "command"
	KindProbe     Kind = "probe"
	KindProgress  Kind = "progress"
	KindSize      Kind = "size"
	KindVariable  Kind = "variable"
	KindMove      Kind = "move"
	KindWarning   Kind = "warning"
	KindError     Kind = "error"
	KindNote      Kind = "note"
	// The app-wide events the pages read: a scan's start, its periodic progress, its end with the
	// counts. They carry no job id and are kept in the log, not a job's record.
	KindScanStart    Kind = "scan.start"
	KindScanProgress Kind = "scan.progress"
	KindScanEnd      Kind = "scan.end"
)

var knownKinds = map[Kind]bool{
	KindRunStart: true, KindRunEnd: true, KindStepStart: true, KindStepEnd: true,
	KindDecision: true, KindCommand: true, KindProbe: true, KindProgress: true,
	KindSize: true, KindVariable: true, KindMove: true, KindWarning: true,
	KindError: true, KindNote: true,
	KindScanStart: true, KindScanProgress: true, KindScanEnd: true,
}

// jobKinds are kept in the job's own record (keep "job") rather than only in the log.
var jobKinds = map[Kind]bool{
	KindRunStart: true, KindRunEnd: true, KindStepEnd: true, KindProbe: true,
}

// historyKinds are the kinds that belong to a job's history. None exist yet; add them
// here when the History work lands. They keep "job" like jobKinds.
var historyKinds = map[Kind]bool{}

// Keep says where an event is kept long-term. Emit sets it from the kind.
type Keep string

const (
	KeepLog Keep = "log"
	KeepJob Keep = "job"
)

func keepFor(k Kind) Keep {
	if jobKinds[k] || historyKinds[k] {
		return KeepJob
	}
	return KeepLog
}

// Command is a copyable command line for a step: argv for the machine, line for the person.
type Command struct {
	Argv []string `json:"argv,omitempty"`
	Line string   `json:"line,omitempty"`
}

// Event is one row of the job record or the app-wide log. Field names are the JSON names in logging.md §3.
type Event struct {
	Seq        int64          `json:"seq"`
	TS         time.Time      `json:"ts"`
	JobID      int64          `json:"jobId,omitempty"`
	Attempt    int            `json:"attempt,omitempty"`
	Level      Level          `json:"level"`
	Kind       Kind           `json:"kind"`
	Code       string         `json:"code,omitempty"`
	Source     string         `json:"source,omitempty"`
	StepID     string         `json:"stepId,omitempty"`
	StepType   string         `json:"stepType,omitempty"`
	StepName   string         `json:"stepName,omitempty"`
	StepIndex  int            `json:"stepIndex,omitempty"`
	LibraryID  string         `json:"libraryId,omitempty"`
	WorkerID   string         `json:"workerId,omitempty"`
	Message    string         `json:"message,omitempty"`
	Port       string         `json:"port,omitempty"`
	PortLabel  string         `json:"portLabel,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	DurationMS int64          `json:"durationMs,omitempty"`
	Command    *Command       `json:"command,omitempty"`
	SizeBefore int64          `json:"sizeBefore,omitempty"`
	SizeAfter  int64          `json:"sizeAfter,omitempty"`
	NextAction string         `json:"nextAction,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Keep       Keep           `json:"keep"`
}

// validate refuses an unknown kind or level. Emit calls it before anything is stored or sent.
func (e Event) validate() error {
	if !knownKinds[e.Kind] {
		return fmt.Errorf("events: unknown kind %q", e.Kind)
	}
	if e.Level.rank() < 0 {
		return fmt.Errorf("events: unknown level %q", e.Level)
	}
	return nil
}

// Filter selects events for a subscriber or a Query. Each zero field matches everything: JobID 0 is every job
// and the app, Attempt 0 every attempt, MinLevel "" every level, and a zero Since or Until is no bound. Since
// and Until are inclusive, compared at millisecond precision as the table stores them. AfterSeq keeps only
// events whose seq is greater. Kind "" is every kind. An unknown MinLevel matches nothing, as Query refuses it.
type Filter struct {
	JobID    int64
	Kind     Kind
	Attempt  int
	MinLevel Level
	Source   string
	StepID   string
	Since    time.Time
	Until    time.Time
	AfterSeq int64
}

func (f Filter) match(e Event) bool {
	if f.JobID != 0 && e.JobID != f.JobID {
		return false
	}
	if f.Kind != "" && e.Kind != f.Kind {
		return false
	}
	if f.Attempt != 0 && e.Attempt != f.Attempt {
		return false
	}
	if f.MinLevel != "" && (f.MinLevel.rank() < 0 || e.Level.rank() < f.MinLevel.rank()) {
		return false
	}
	if f.Source != "" && e.Source != f.Source {
		return false
	}
	if f.StepID != "" && e.StepID != f.StepID {
		return false
	}
	if !f.Since.IsZero() && e.TS.UnixMilli() < f.Since.UnixMilli() {
		return false
	}
	if !f.Until.IsZero() && e.TS.UnixMilli() > f.Until.UnixMilli() {
		return false
	}
	if f.AfterSeq > 0 && e.Seq <= f.AfterSeq {
		return false
	}
	return true
}
