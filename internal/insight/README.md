# Radar R1 observation contracts

This package is pure. It imports only the standard library, does not read a clock globally and performs no IO. R1 projects inventory, not signals or incidents. R2 will implement detectors. No alert, score or health conclusion is produced here.

## Consumer entry point

An existing source may implement `ObservationSource` with `Observations(after Cursor) ObservationBatch`. This optional interface does not change `data.Source`. Type-assert the interface once; its absence means observations are unavailable. Opening a view must not start collection.

Keep the returned `Cursor`. An equal cursor returns `Unchanged=true`, no pods and no deep copies. A different cursor returns a complete current projection (`FullInventory=true`), not a delta stream. `Reset` means start a new baseline because there is no usable cursor for this source. `SkippedInventoryRevisions` reports coalesced intermediate publications. Even zero skipped revisions does not guarantee every Kubernetes transition was delivered. There is no replay log or historical tombstone journal in R1.

Only `Coverage.Inventory.State == CoverageComplete` makes absence authoritative within `Namespace`. An empty namespace denotes the source's connected all-namespace scope. Partial, stale, denied, collecting and unavailable snapshots must not resolve incidents or prove deletion. A full complete empty snapshot replaces the inventory with no pods. Consumers compare entity keys to recognize removals; they must not infer them from an unchanged response.

`Revisions` reserves distinct Inventory, Metrics, Events, Security and Owners revisions. Only Inventory advances in R1. All other channels explicitly report unavailable. Network is disabled and no proxy request or permission is added. Direct owner references are observed Pod metadata; they are not resolved ReplicaSet or Deployment relationships. NodeName is only an observed assignment, not a resolved node UID or node health assertion.

## Identity and times

`SourceIdentity` includes SessionID, Context and Generation. Generation is the source epoch. The kube adapter uses its process-unique source generation as the session identifier; reconstructing a source produces a new identity and baseline. `EntityRef` contains an `EntityKey` (source, group, version, kind, namespace, name, UID) and an opaque ResourceVersion. RV is never ordered numerically. Do not key detectors by display names or include RV in their entity identity.

`Inventory.Publish` preserves local observation timestamps on a duplicate UID/RV, even if the clock advances. A new RV updates ReceivedAt, retaining FirstObservedAt. A recreated UID starts new metadata and a new entity key; no restart counter or first-observed timestamp transfers from the old UID. Source changes reset revision and baseline. Removed entities are not retained indefinitely.

`Clock.Now` is injected. `ClockFunc` adapts the backend's existing Options.Now. Local ObservedAt and ReceivedAt mean cache consumption/publication, not Kubernetes event time. Local time is clamped against backwards movement within one source. CreatedAt, DeletionAt, StartedAt, condition times and termination times are source timestamps, never parsed from Age, Since or Finished display strings. Zero source timestamps mean not reported; detectors must validate freshness, future timestamps and counter validity before deriving durations or rates. FirstObservedAt is the first publication of the entity in the current inventory lifetime, not proof of its creation or a status transition.

## Typed evidence

`PodObservation`, `ContainerObservation`, `Termination`, `Condition` and `OwnerRef` contain evidence without presentation age strings. `PodPhase`, `ContainerPhase`, `ContainerKind`, `Reason`, `ConditionKind` and `Truth` are typed values. Consumers use these constants for logic. ReasonText and Message are sanitized display evidence, not detector inputs. Unrecognized reasons are ReasonOther; missing reasons are ReasonUnknown.

Regular, init, restartable init sidecar and ephemeral containers stay distinct. Missing container status is `StatusObserved=false`, phase unknown and Ready/Started unknown. Current and last termination are independent nullable values. Exit code 137 alone does not map to OOMKilled. Missing status does not mean healthy. UID/RV missing means the pod is omitted with explicit partial coverage.

## Ownership and limits

`Inventory` requires a non-nil injected clock and exclusive ownership or caller synchronization. The kube adapter protects it with the existing Source mutex. Publish clones input evidence. Changed reads return detached snapshots, including nested termination pointers. Consumers may retain a snapshot without observing later mutation. The current inventory retains at most MaxInventoryPods (10000), ordered by namespace, name and UID, with observed/total counts and a visible truncation reason. It does not accumulate deleted UIDs.

Projection runs only during backend rebuild from the existing informer batch. Per-pod conversion is cached by UID/RV. Reading observations does not select a pod, call Metrics, read logs, start wide events, request nodes/proxy, or contact Kubernetes. Replay and UI frame reads do not advance revisions.

# Radar R2 operational state machines

`NewEngine(Clock, OperationalConfig)` validates and copies an immutable configuration. `DefaultOperationalConfig` defines the initial thresholds below. A different configuration requires an explicit new engine and baseline in R2; there is no live rule editor or silent partial configuration mutation. Rule-scoped editing and migration are not implemented here.

`Engine.Step(ObservationBatch)` consumes R1 snapshots. `Engine.Advance()` evaluates elapsed timers using the last accepted complete inventory without manufacturing an observation, restart or termination. Deliver current coverage before calling Advance; suspended coverage never resolves a signal. `Engine.Snapshot()` reads detached value records without reading the clock. The engine has exclusive ownership and is not concurrently callable.

`SignalSnapshot` contains source identity, configuration hash, OperationalRuleVersion, collection coverage and deterministically sorted `Signal` records. `Signal` contains stable episode ID, rule/config version, entity/container identity, state/severity/peak severity, Preexisting, local first/last observation times, resolution time, per-rule coverage, typed SuspensionReason and numeric/timestamp SignalEvidence. Normal and candidate records are included so consumers can distinguish a known normal baseline from missing coverage. They are not pending alerts. IDs include source, entity UID, container name/kind, rule, rule version, configuration and episode number. Sorting is severity descending, last observation descending, ID ascending. Equal input, configuration and injected clock produce equal output.

| Rule | Activation | Recovery |
| --- | --- | --- |
| Restarts | Observed positive deltas >=3 Warning, >=10 High in the trailing 5min | No new restart for 5min |
| CrashLoop | Same waiting reason for >=30s and >=2 distinct pod RV observations, High | Running and Ready continuously for 60s |
| ImagePull | ErrImagePull or ImagePullBackOff continuously for >=2min Warning, >=10min High | Running and Ready continuously for 60s |
| Pending | Pending or nonterminal ContainerCreating observed for >=5min Warning, >=15min High | Neither condition for 60s |
| OOM | Newly observed distinct OOMKilled termination with usable FinishedAt, High | No new OOM for 5min and Running/Ready for 60s |
| Evicted | Observed transition to Evicted, Warning | No new eviction for 5min, even if the terminal pod stays Evicted |

All boundaries are inclusive. Restart windows exclude deltas exactly 5min old. A counter on first observation establishes a baseline and is never counted as a rate. Initial persistent problems are explicitly Preexisting. Initial OOM/eviction is shown as preexisting evidence, not a newly witnessed transition. A pod first appearing later in the session also starts a baseline; this is not proof that the pod was created at that moment. Pod creation time is displayed as evidence and never used as Pending onset. NodeName distinguishes scheduling from creation after assignment. Per-node escalation for three evictions belongs to incident correlation in R5, not these per-entity R2 signals.

Unknown/missing status, negative counters, counter regression, unusable OOM timestamps and buffer exhaustion suspend affected rules with explicit partial coverage. A skipped inventory revision or incomplete source coverage suspends all existing rules. Advance cannot recover them. The next complete distinct inventory publication restores a conservative baseline: uncertain deltas are discarded and recovery intervals restart. Active signals keep their episode IDs and do not become resolved merely because data returned. Coverage on SignalSnapshot is collection-level; individual Signal coverage can still be partial because of a counter/status/timestamp problem.

A duplicate UID/RV does not add an observation, increment a restart window or invent another OOM. Timer progress with an advanced clock is allowed under complete connected coverage, but the two-observation CrashLoop requirement cannot be satisfied by replay or Advance. An older cursor from the same source cannot rewind state. Unknown source time is not replaced by local time: an OOM without FinishedAt is suspended; timestamps beyond the configurable 30s future tolerance are suspended. Current and last OOM records with the same timestamp count once; a cached last termination does not become another OOM just because an unrelated restart increments the counter. Exit 137 alone is not OOM evidence.

A source identity change starts a new baseline. Recreated UIDs never inherit old counters, onset times or IDs. Complete authoritative inventory removes vanished entity machines without producing a Resolved event: disappearance is not recovery. R2 retains no incident history; R5 may retain bounded historical evidence separately. Missing containers within a still-observed pod are suspended. Machines are bounded by current inventory entities/containers. Restart deltas are bounded to 256 per rule by default; overflow suspends instead of silently claiming an exact rate.

This delivery does not collect metrics, create incidents, correlate nodes/workloads, acknowledge or silence anything, import Security alerts, or change UI/navigation/permissions. Those remain later Radar PRs. No score or intrusion claim is introduced.

Snapshots return at most MaxOperationalSignals (20000) sorted records. TotalSignals, OmittedSignals and per-rule Counts preserve exact counts when display records exceed the cap. This cap does not remove the underlying detector baselines and does not turn omitted evidence into health. R5 can compact these counts into incident summaries. Current-input detector memory and projection memory must be measured separately from snapshot payload.
