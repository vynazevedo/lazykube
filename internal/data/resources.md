# Resource browser contracts

`ResourceBrowser` is an optional source interface. Fetch `Catalog(ctx, false)` in a command, never in rendering. Expanding a category reads the cached catalog and must not call `Subscribe`. Discovery does not authorize instance reads. `Catalog(ctx, true)` explicitly refreshes the five minute cache. `Lookup` distinguishes served, absent, denied, partial discovery and unknown. Subresources are not leaves. Preferred versions are marked; selection remains explicit.

Build `ResourceSubscriptionKey` using the catalog's Session and Generation, the exact served GVR, selected namespace, selectors and `ResourceProjectionVersion`. A namespace-restricted session cannot subscribe to all namespaces. Secrets are rejected by this interface until the separate metadata opt-in source exists. No resource URL is taken from an object. No proxy subresource is supported.

`Subscribe` shares an entry for identical keys. Each handle has its own coalescing `Changes` channel. Call `Page("", 500)` initially and on notification. Follow `Continue` for more rows of the same revision. A cursor from another revision returns stale coverage without rows; restart from the first page. `Refresh` explicitly retries a snapshot, including list-only or denied entries. Call `Cancel` on route exit. Releasing the last consumer cancels requests and discards the store immediately. There is no idle cache or persistence. Context cancellation also releases the handle. Closing the source terminates the pool and prevents reuse across sessions.

`ResourcePage.Total` is the retained count, not an estimate of invisible objects. Only complete coverage establishes a complete inventory in the requested scope. List-only means a point-in-time list, with ObservedAt. Watch-only remains partial because the initial population is unknown. Forbidden, a closed watch, 410, malformed or repeated continue tokens, invalid metadata and exhausted budgets never become a successful empty inventory. Failed watch continuity triggers a fresh paginated list before complete coverage can return. Error reasons are fixed messages without server payloads.

## Workloads

Rows carry `WorkloadSummary` for pods, deployments, StatefulSets, DaemonSets, Jobs, CronJobs and ReplicaSets. Counters carry `Known`; absent status must not become a fabricated zero. Controllers compare generation with observedGeneration before declaring readiness. Jobs require terminal conditions for failed/completed states; a failed retry counter alone is not terminal failure. CronJobs expose schedule, suspension, active count and last schedule without inferring missed runs. `WorkloadDistribution` is maintained with the store and included in every page, independent of pagination. Its denominator is objects of that kind, never child replicas. Counts have the same coverage and revision as their page. They are not health or security scores.

Pods and deployments use existing informer adapters, as do nodes, namespaces and events. No dynamic pod watch or per-row GET is created. Namespace and event adapters explicitly report partial continuous coverage where the existing cache does not expose equivalent health. The first version projects metadata and workload summaries only. YAML, details, owner relations and specialized network/storage columns require separate bounded contracts; they are not implied by discovery.

## Limits

- Six new dynamic watches, two concurrent LISTs, twelve active subscription keys.
- LIST pages of 500, request deadline ten seconds, retries with bounded backoff and jitter up to thirty seconds. Forbidden does not loop automatically.
- Ten thousand retained rows per subscription, thirty thousand across the pool, including borrowed projections. Overflow stops that subscription's watch and exposes partial coverage. There is no unbounded raw informer store behind the displayed limit.
- A projected row, including its owned strings, is at most 1 KiB. Raw objects, annotations, ConfigMap values, event messages and arbitrary CR columns are not retained in these rows.
- Production transport limits LIST/discovery bodies to 8 MiB and individual JSON objects or watch envelopes to 2 MiB while reading. An oversized response is rejected before unbounded decoding. These limits do not apply to the independent, already existing informer caches.
- The resource browser has a separate 64 MiB incremental memory target. Row caps include map/key overhead headroom; the 10k pods plus 10k EndpointSlices benchmark measures retained heap and allocation volume. This is not a promise that the entire application, Radar or preexisting caches occupy 64 MiB.

The tree and Overview consume this interface asynchronously. They must preserve coverage text, distinguish unknown from zero and avoid keeping every page from every revision. No screen, key or action is added by the data contract itself.
