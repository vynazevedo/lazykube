package insight

import "time"

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type SourceIdentity struct {
	SessionID  string
	Context    string
	Generation uint64
}

type EntityKey struct {
	Source    SourceIdentity
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

type EntityRef struct {
	EntityKey
	ResourceVersion string
}

type Revisions struct {
	Inventory uint64
	Metrics   uint64
	Events    uint64
	Security  uint64
	Owners    uint64
}

type Cursor struct {
	Source    SourceIdentity
	Revisions Revisions
}

type CoverageState uint8

const (
	CoverageUnavailable CoverageState = iota
	CoverageCollecting
	CoverageComplete
	CoveragePartial
	CoverageDenied
	CoverageStale
	CoverageDisabled
)

type Coverage struct {
	State    CoverageState
	Reason   string
	Observed int
	Total    int
}

type SourceCoverage struct {
	Inventory Coverage
	Metrics   Coverage
	Events    Coverage
	Security  Coverage
	Owners    Coverage
	Nodes     Coverage
	Network   Coverage
}

type ObservationSource interface {
	Observations(after Cursor) ObservationBatch
}

type ObservationBatch struct {
	Cursor                    Cursor
	ObservedAt                time.Time
	Namespace                 string
	Synthetic                 bool
	Unchanged                 bool
	FullInventory             bool
	Reset                     bool
	SkippedInventoryRevisions uint64
	Coverage                  SourceCoverage
	Pods                      []PodObservation
}

type PodPhase uint8

const (
	PodUnknown PodPhase = iota
	PodPending
	PodRunning
	PodSucceeded
	PodFailed
)

type Reason uint8

const (
	ReasonUnknown Reason = iota
	ReasonOther
	ReasonCrashLoopBackOff
	ReasonOOMKilled
	ReasonImagePullBackOff
	ReasonErrImagePull
	ReasonContainerCreating
	ReasonPodInitializing
	ReasonEvicted
	ReasonNodeLost
	ReasonCompleted
)

type Truth uint8

const (
	TruthUnknown Truth = iota
	TruthFalse
	TruthTrue
)

type ConditionKind uint8

const (
	ConditionOther ConditionKind = iota
	ConditionReady
	ConditionScheduled
	ConditionInitialized
	ConditionContainersReady
)

type Condition struct {
	Kind             ConditionKind
	Name             string
	Status           Truth
	ReasonText       string
	Message          string
	LastProbeAt      time.Time
	LastTransitionAt time.Time
}

type ContainerKind uint8

const (
	ContainerRegular ContainerKind = iota
	ContainerInit
	ContainerSidecar
	ContainerEphemeral
)

type ContainerPhase uint8

const (
	ContainerUnknown ContainerPhase = iota
	ContainerWaiting
	ContainerRunning
	ContainerTerminated
)

type Termination struct {
	Reason     Reason
	ReasonText string
	Message    string
	ExitCode   int32
	Signal     int32
	StartedAt  time.Time
	FinishedAt time.Time
}

type ContainerObservation struct {
	Name               string
	Kind               ContainerKind
	Image              string
	StatusObserved     bool
	Ready              Truth
	Started            Truth
	RestartCount       int32
	Phase              ContainerPhase
	Reason             Reason
	ReasonText         string
	Message            string
	StartedAt          time.Time
	CurrentTermination *Termination
	LastTermination    *Termination
}

type OwnerRef struct {
	Entity     EntityRef
	Controller bool
}

type PodObservation struct {
	Entity          EntityRef
	CreatedAt       time.Time
	DeletionAt      time.Time
	StartedAt       time.Time
	FirstObservedAt time.Time
	ReceivedAt      time.Time
	Phase           PodPhase
	Reason          Reason
	ReasonText      string
	Message         string
	NodeName        string
	Owners          []OwnerRef
	Conditions      []Condition
	Containers      []ContainerObservation
}
