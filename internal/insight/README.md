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
