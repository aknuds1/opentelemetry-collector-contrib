# Preserving Prometheus Job and Instance in OTLP Translation

# Problem Statement

Historically, OpenTelemetry specifications have treated Prometheus **job** / **instance** and OpenTelemetry **service.name** / **service.instance.id** as interchangeable representations of the same underlying identity. In practice, they serve fundamentally different purposes:  
\- **Prometheus job and instance** identify the scrape configuration and target address.  
\- **OpenTelemetry service.name, service.namespace, and service.instance.id** identify the logical application entity.

## Practical Issues Today

1\. **Data Loss on Scrape**: When scraping a Prometheus endpoint that exposes a target\_info metric containing service.name and service.instance.id, the scraper is currently forced to drop either the Prometheus scrape identity (job/instance) or the OpenTelemetry semantic identity (service.name/service.instance.id).  
2\. **Prometheus Backend Expectations**: Users pushing OTLP to Prometheus expect to query for service.name and service.instance.id as standard resource labels, but today must explicitly configure server-side flags (e.g., keep\_identifying\_resource\_attributes=true) to prevent them from being stripped or unconditionally converted into job and instance.  
3\. **Pollution of service.name in Kubernetes**: Deriving service.name directly from job often yields non-standard names (e.g., prometheus\_simple/10.42.0.15:8080), breaking correlation with pod logs and OTel SDK traces.

\---

# Requirements

1\. **Separate Storage**: Store job/instance and service.name/service.namespace/service.instance.id separately as OpenTelemetry Resource Attributes when both sets of identifiers exist.  
2\. **Universal Join Key**: Always ensure a job/instance pair is available when translating to Prometheus formats (e.g., in aggregated exporters or PRW), even when OTLP is ingested without explicit job/instance resource attributes.  
3\. **Queryable Resource Attributes**: Allow users to query for service.name and service.instance.id like any other OTel resource attribute when present.  
4\. **Non-Breaking Server Compatibility**: Avoid breaking changes to Prometheus Server default behavior prior to a major version bump.

\---

# Proposed Design

## 1\. Core Rules

\- **Preserve Semantic Identity**: service.name, service.namespace, and service.instance.id from target\_info are preserved as Resource Attributes and are **never dropped**.  
\- **Defaulting service.\* from job/instance**: When service.name or service.instance.id are absent on target\_info, receivers **MAY** default them from job and instance.  
\- **Opt-Out for Defaulting**: If service.name and service.instance.id are defaulted to job and instance, implementations **MUST** provide a configuration toggle allowing users to disable this behavior.  
\- **Aggregated Exporter Fallback (OTLP → Prometheus)**: When exporting metrics from multiple resources, aggregated exporters look up the stored job and instance resource attributes first. If absent, they fall back to synthesizing job from \<service.namespace\>/\<service.name\> (or \<service.name\>) and instance from service.instance.id.

\---

## 2\. Prometheus Server Backwards Compatibility (honor\_labels on OTLP Endpoint)

To avoid breaking existing Prometheus Server OTLP ingestion deployments prior to a major release:

1\. **OTLP Endpoint honor\_labels Configuration**:  
   \- Prometheus Server can add an honor\_labels configuration option to its **OTLP endpoint configuration**.  
   \- When **honor\_labels=true**, the OTLP endpoint respects incoming job and instance resource attributes on the OTLP Resource and uses them directly as the metric's job and instance labels.  
   \- When **honor\_labels=false** (default for the current major version), the OTLP endpoint preserves existing backwards-compatible behavior by deriving job and instance from service.namespace/service.name and service.instance.id.  
2\. **Future Major Version Defaults**:  
   \- In a future Prometheus Server major release, both honor\_labels (on the OTLP endpoint) and keep\_identifying\_resource\_attributes can switch their default setting to true.  
\---

## 3\. Storing Scrape Identity: Bare (job / instance) vs. Namespaced (prometheus.job / prometheus.instance)

When preserving the original scrape identity on the OpenTelemetry Resource alongside service.\* attributes, both options ultimately translate back to job and instance labels when exported from OTLP → Prometheus.

### Option A: Bare (job and instance) (Proposed)

\- **Resource Attributes in OTLP**: job and instance.  
\- **Collector / OTTL UX**: Users writing OTTL or Collector processors can naturally inspect and modify job and instance on the OTel Resource without learning a special prefix.  
\- **Consistency**: Matches how all other un-namespaced Prometheus labels (container, pod, namespace) are mapped to resource attributes without needing formal semantic convention registration.

### Option B: Namespaced (prometheus.job and prometheus.instance)

\- **Resource Attributes in OTLP**: prometheus.job and prometheus.instance.  
\- **Collector / OTTL UX**: Users modifying metrics in OTel Collector processors would need to know to target prometheus.job instead of job.

\---

# Summary of Translation Flows

| Direction | Input | Resource Attributes Stored | Output Metric Labels |
| :---- | :---- | :---- | :---- |
| **Prometheus → OTLP** | Scrape job/instance \+ target\_info | job and instance stored alongside service.name / service.instance.id | N/A (OTLP Resource) |
| **OTLP → Prometheus (Aggregated)** | OTLP Resource | Reads stored job/instance (fallback to service.\*) | job and instance labels emitted directly on exported metrics |

Combinations (Prometheus to OTLP)  
	To avoid writing so much, let's just look at job and service.name

| Input series | Input target\_info | Before PR 4956 | After PR 4956 |
| :---- | :---- | :---- | :---- |
| none | none | Error as [service.name](http://service.name) and [service.instance.id](http://service.instance.id) MUST be filled. (prom receiver can guess from target, so there's a chance this works) | Error as job and instance  MUST be added to resource attributes |
| job | none | not explicit, but de-facto become [service.name](http://serice.name) r.a. | explicit, job becomes job r.a. (BREAKING) |
| job | job | same as above | same as above |
| job, service.name | none | not explicit, job becomes [service.name](http://service.name) and r.a.  By the spec, the [service.name](http://service.name) label MUST be r.a. , so this is conflict, but no resolution. OTel collector prom receiver: source [service.name](http://service.name) becomes [service.name](http://service.name) data point attribute (OTEl coll), violating the spec. | job becomes r.a. (BREAKING).  source [service.name](http://service.name) becomes [service.name](http://service.name) data point attributes, as there's no longer a rule that explicitly makes them r.a. |
| job | job, service.name | Spec says that both job and [service.name](http://service.name) map to [service.name](http://service.name) r.a. No resolution of conflict. OTel collector prom receiver: [service.name](http://service.name) from target\_info prevails over job | no conflict, new job r.a. (BREAKING-ISH) |
|  |  |  |  |

Combinations (OTLP to Prometheus)  
           To avoid writing so much, let's just look at job and service.name

| Input data point attributes | Input resource attributes | Before PR 4956 | After PR 4956 |
| :---- | :---- | :---- | :---- |
| none | service.name | becomes job on metric and target\_info | becomes job on metric and target\_info |
| none | job | not added to metric, remains job in target\_info, no special handling | added to job on metric and target\_info (BREAKING-ISH) |
| job | service.name | not explicit [service.name](http://service.name) becomes job and overwrites attribute job, on metric and target\_info | not explicit, [service.name](http://service.name) becomes job and overwrites attribute job, on metric and target\_info |
| none | job, service.name | [service.name](http://service.name) becomes job on both metric and target\_info, overwrites job r.a. | job r.a. put on metric and in target\_info, [service.name](http://service.name) only in target into (no overwrite, BREAKING) |

\---

# Option C: Namespaced Scrape Identity Override

Option C is a standalone alternative that uses the same namespaced Resource
attribute names as Option B but does not depend on Option B or any other design
section. The rules below are the complete design.

It addresses Practical Issues 1 and 3 by storing Prometheus scrape identity
separately from semantic service identity and by never deriving `service.*`
from `job` or `instance`. It does not solve Practical Issue 2 for arbitrary
native OTLP data, as this is already handled by Prometheus'
`keep_identifying_resource_attributes` option.

## Interoperability Contract

- A **producer** is a Prometheus or OpenMetrics to OTLP translator that emits
  Option C attributes. A **consumer** is an OTLP to Prometheus translator that
  synthesizes Resource-level `job` and `instance` identity, such as Prometheus
  OTLP ingestion or an aggregated Prometheus exporter.
- Option C defines three Resource **control attributes**:
  `prometheus.scrape.identity.version`, `prometheus.job`, and
  `prometheus.instance`. The latter two form the **reserved pair**.
- Consumer recognition of the control attributes MUST be gated behind an
  implementation-specific option that defaults to disabled and can be scoped to
  an input endpoint or pipeline. When recognition is disabled, all three
  attributes receive complete legacy handling. The remaining consumer rules in
  this section apply only when recognition is enabled.
- An **active v1 tuple** has `prometheus.scrape.identity.version` set to the
  string `"1"` and both members of the reserved pair present as non-empty
  strings. A consumer MUST apply the Option C identity override to all metric
  points associated with a Resource only when that Resource has an active v1
  tuple.
- Same-named metric data point attributes do not activate Option C and remain
  ordinary metric attributes. Labels with these names on `target_info` likewise
  cannot activate Option C or overwrite producer-generated control attributes.
- For an active v1 Resource, the control attributes are consumed as translation
  control and identity. By default, they are not emitted under their translated
  names as `target_info` metadata labels or ordinary metric labels.
- The **covered service attributes** are `service.name`, `service.namespace`,
  and `service.instance.id`. The round-trip guarantee covers an attribute only
  when its value comes from valid associated `target_info` and is a non-empty
  string.
- A **supported ordinary metric point** is a non-`target_info` point whose
  metric type, value, and timestamp both translators otherwise accept. Option C
  changes its identity and metadata translation, not metric-type support.
- A **source translation unit** is one original scrape transaction or one
  application transaction whose boundary an upstream sender establishes before
  transport batching, queueing, sharding, WAL persistence, retries, or request
  assembly. For each identity pair, the unit contains one coherent
  `target_info` snapshot, which may be absent or inactive, that applies to all of
  that pair's ordinary points in the unit. A transport request or batch does not
  become a source translation unit merely because it was submitted as one. A
  **producer translation unit** is the unit presented to the Option C producer:
  the same scrape transaction for direct scraping, or one received Prometheus
  batch or request. `target_info` association is limited to the producer
  translation unit; Option C does not retain target metadata for later units.
- An **Option C batch** is the set of active v1 `ResourceMetrics` emitted
  together in one OTLP `Metrics` payload from one producer translation unit. A
  full-profile path preserves this producer-defined boundary. The boundary is
  not encoded by another control attribute and cannot be reconstructed after an
  intermediary splits or coalesces it.
- An **input atomic-delivery capability** is a configured end-to-end property of
  a Remote Write input path. It guarantees that one request contains exactly
  one complete, pre-established source translation unit, including all of its
  ordinary series and `target_info`, and that sender batching, sharding, queues,
  WAL persistence, retries, and concurrency neither split that unit nor mix it
  with another. A receiver cannot infer this capability or the source-unit
  boundary from the contents of one request.
- An **identity group** is all active v1 Resources in one Option C batch that
  have the same reserved pair.
- An **active canonical `target_info` slot** is the final generated
  `target_info` metric name and exact final `job` and `instance` pair after
  metric-name handling and the Option C identity override. It is scoped to the
  complete output operation. A protocol-required stale marker that retires a
  previously emitted canonical label set is lifecycle output, not an active
  slot occupant.
- A **bounded diagnostic** means at most one warning or error for the specified
  series, Resource, identity group, active canonical `target_info` slot, or
  identity-and-key combination in the relevant translation or export operation,
  not one diagnostic per metric point.

## Prometheus to OTLP

- Producer emission MUST be gated behind an implementation-specific option that
  defaults to disabled. When disabled, perform the complete existing
  translation and emit no control attributes. The rules below apply when it is
  enabled.
- For each source series, first use its final `job` and `instance` label values
  after scrape label conflict handling and metric relabeling. If either value is
  empty, fill only that missing value from scrape-target context when available.
  Do not otherwise derive or rewrite either value.
- If either value is still empty, fail that ordinary series, report one bounded
  diagnostic for it, and emit neither a partial reserved pair nor partial OTLP
  output for the series. The input-specific response behavior is defined below.
- Group supported ordinary metric points by the resulting exact pair. Store the
  string `"1"` as `prometheus.scrape.identity.version` and the values as
  `prometheus.job` and `prometheus.instance` Resource attributes. Emit all three
  control attributes together, and do not also store the source `job` or
  `instance` as metric data point attributes.
- Emit the active v1 Resources produced from one producer translation unit
  together as one Option C batch. A producer or intermediary that splits or
  coalesces these batches can participate in the identity profile, but not the
  full profile.
- Associate `target_info` only by the same exact normalized pair within the
  producer translation unit. An incomplete `target_info` identity cannot
  associate and produces a bounded diagnostic.
- For each associated `target_info` series, select its greatest-timestamp
  sample. If several samples share that timestamp, they represent one state
  only when they are all stale or are all non-stale with value `1`. A stale
  state is inactive. A non-stale value other than `1`, or a tie mixing stale and
  non-stale or differing values, is malformed or conflicting; that series
  contributes no metadata and produces a bounded diagnostic. A series without
  samples is also inactive and contributes no metadata.
- Within Option C translation, populate covered service Resource attributes only
  by merging active associated `target_info` series, independently for each key;
  do not synthesize them from scrape identity. If no series has a non-empty
  value, leave the Resource attribute absent. If all present values agree, store
  that value. If multiple distinct values occur, omit only the conflicting
  Resource attribute and report one bounded diagnostic; other unambiguous
  covered attributes remain eligible.
- Labels named after any control attribute on `target_info` are ignored as
  metadata and cannot overwrite or supply the active tuple. Other target
  metadata retains its existing handling but is outside the guarantee.
- Consume `target_info` rather than translating it as an OTLP metric. A producer
  translation unit containing only `target_info` emits no empty
  `ResourceMetrics`.

## OTLP to Prometheus

- When consumer recognition is disabled, preserve complete legacy identity,
  metadata, promotion, and collision behavior for every Resource, regardless of
  the presence or value of any control attribute.
- When `prometheus.scrape.identity.version` is absent, do not reserve or
  suppress any control attribute. Preserve complete legacy identity, metadata,
  promotion, and collision behavior even if one or both pair attributes are
  present.
- When the version attribute is present but is empty, non-string, or not `"1"`,
  fail the Resource without emitting its points and report one bounded
  diagnostic. Do not guess the semantics of an unknown future version.
- When the version is `"1"` but the pair is partial, empty, or non-string,
  report one bounded diagnostic, suppress all three control attributes by
  default, and derive both identity labels through the complete legacy identity
  path. Preserve legacy handling for non-control metadata, but never combine
  one reserved identity value with one legacy value.
- For an active v1 tuple, use `prometheus.job` and `prometheus.instance`
  atomically as the `job` and `instance` labels on every ordinary metric and
  generated `target_info` for that Resource. The pair is authoritative over
  conflicting point-level or exporter-added identity, and neither value is
  derived from `service.*`.
- Before generating `target_info`, group all active v1 Resources in the received
  Option C batch by reserved pair. For each Resource, compute the candidate
  `target_info` labels after applying existing Resource-attribute selection,
  value conversion, label-name translation, and collision rules, except for the
  overrides defined here. Exclude all control attributes and add the reserved
  pair separately as `job` and `instance`.
- When `target_info` generation is enabled, every present covered service
  attribute with a non-empty string value is a candidate regardless of
  `keep_identifying_resource_attributes`. For each final label name other than
  `job` and `instance`, include the label once when all present candidates
  supply one value. If candidates supply multiple values, omit that final label
  and report one bounded diagnostic for the identity group and label name.
  Absence from some Resources does not conflict with one value supplied by
  other Resources.
- An identity-profile consumer applies the same merge-and-omit rule to
  conflicting covered service attributes. A full-profile consumer instead
  requires every Resource in the identity group to have identical presence and,
  when present within the guaranteed domain, the same non-empty string value for
  each covered service attribute. A difference is a full-profile validation
  failure.
- When an identity-profile output operation coalesces several Option C batches,
  combine identity groups that map to the same active canonical `target_info`
  slot using the same merge-and-omit rule before generating output. The full
  profile continues to prohibit coalescing Option C batches.
- Generate at most one active canonical `target_info` label set for each slot in
  one output operation. The full profile generates that series even when the
  group has no metadata beyond the active tuple. Outside the full profile, an
  active tuple alone does not require `target_info`. No control attribute is
  included on generated `target_info` by default.
- Construct the canonical samples once per slot. For pull, expose one sample
  with value `1` and no explicit timestamp. For Remote Write output, each
  Resource contributes the greatest timestamp among its supported ordinary
  points. For direct OTLP ingestion, each Resource contributes its existing
  schedule from its earliest supported ordinary-point timestamp through its
  latest, inclusive, at half the configured or default lookback-delta interval.
  Union the contributed timestamps, deduplicate equal timestamps, and sort them
  when the output carries timestamps. A timestamp is usable when it is nonzero
  and accepted by the output's existing `target_info` timing rules. Equal
  generated samples removed by this canonicalization are not output collisions.
  If a timestamp-carrying output has no usable canonical timestamp, an
  identity-profile output omits canonical `target_info`; a full-profile output
  fails validation for the identity group.
- When canonical `target_info` will be generated, reserve its active slot across
  the complete output operation. An ordinary or legacy series that maps to the
  same final metric name and pair is a semantic collision regardless of its
  other labels, timestamps, value, or metric-family definition. Never emit it
  alongside the canonical series. An identity-profile output keeps the
  canonical series, drops every noncanonical competitor, and reports one
  bounded diagnostic for the slot. A full-profile output fails the affected
  identity group under the output-specific failure scope. When canonical
  generation is disabled or omitted, the slot is not reserved.
- A stale marker is exempt from active-slot collision only when existing
  lifecycle tracking emits it to retire a no-longer-active label set that was
  previously emitted as canonical `target_info`. Such a marker may coexist with
  a distinct current active canonical series but does not represent active
  metadata. An arbitrary stale ordinary or legacy series receives no exemption.
  Historical series, source `target_info` sample timing, and cross-operation
  cadence remain outside the guarantee.
- Empty or non-string covered service attributes follow existing translation
  behavior but are outside the round-trip guarantee.
- Settings that disable `target_info` remain authoritative. They do not affect
  active v1 identity, but the service-metadata guarantee no longer applies. A
  setting that renames `target_info` likewise makes the path identity-only.
- Resource-to-ordinary-label conversion remains orthogonal. This includes
  `promote_resource_attributes`, `promote_all_resource_attributes`, and
  equivalent exporter settings. Any control attribute may be explicitly
  promoted according to the existing include, ignore, and collision rules.
  Such promotion emits the attribute under its translated name on ordinary
  metric series, not as additional `target_info` metadata. A resulting label
  set that promotes a control attribute is outside the full round-trip
  guarantee.
- Label-name translation still applies to generated `target_info`. Exact
  service-attribute round-tripping requires the mapping to preserve each
  covered dotted name and to be injective across all labels generated for that
  `target_info` series. A UTF-8-preserving, no-translation strategy avoids
  normalization-induced loss and collisions. If a key is renamed or collides,
  that covered service attribute is outside the guarantee; reserved scrape
  identity remains covered.

## Conformance Profiles

Conformance is a property of an end-to-end path and its configured output mode,
not only of a component binary.

- An **identity-profile path** has Option C producers and consumers that emit or
  recognize the active v1 tuple, enabled consumer recognition, and
  intermediaries that neither alter nor drop the tuple. It guarantees the exact
  `job` and `instance` values on otherwise non-colliding output points but makes
  no service-metadata guarantee. For a pull round trip, the receiving scrape is
  part of the path and MUST preserve those labels.
- A **full-round-trip-profile path** also preserves the individual presence and
  non-empty string values of the covered service attributes. It requires
  enabled, canonical, unrenamed `target_info`; an injective UTF-8-preserving
  label mapping; a protocol or negotiated format that accepts the dotted names;
  preservation of the Option C batch boundary; injective ordinary output
  translation; one active canonical `target_info` label set per slot and a
  usable canonical sample schedule; no explicitly promoted control attributes;
  and no processor that changes covered identity or metadata. A Remote Write
  input path also requires the input atomic-delivery capability.
- The full profile is a batch-snapshot guarantee evaluated independently for
  each preserved Option C batch and output operation. It covers the canonical
  metadata emitted for that operation, not retirement of historical series or
  query-time uniqueness across operations.
- Every Resource in an identity group MUST have identical presence and values
  for each covered service attribute to be eligible for the full profile. A
  conflicting group fails full-profile export with one bounded diagnostic; it
  MUST NOT silently omit the conflict or downgrade the group.
- Other Resource metadata remains outside the guarantee and follows the
  canonical merge-and-omit rule above instead of creating another
  `target_info` series.
- Before full-profile output, translate the complete batch far enough to verify
  its final Prometheus metric names, label sets, timestamps, and metric-family
  definitions. If distinct input points collapse onto the same final series and
  timestamp, even with equal values, or create incompatible definitions for one
  metric family, a noncanonical series occupies an active canonical
  `target_info` slot, or a required canonical sample schedule is empty, the
  affected identity group fails validation. Compatible ordinary samples at
  distinct timestamps may share one series. Equal generated `target_info`
  timestamps are deduplicated before this validation and are not collisions.
- A static configuration or transport that lacks a full-profile capability may
  claim only the identity profile. After a path is configured to provide the
  full profile, a per-batch conflict or limit failure MUST fail as specified
  below and MUST NOT silently downgrade that batch.

## Producer Input Transport and Failure Semantics

These rules apply when producer emission is enabled. When it is disabled, the
producer retains its complete existing input, cache, and response behavior.

### Scrape

- A scrape input is eligible for the full profile only when ordinary series and
  their associated `target_info` are translated from the same scrape
  transaction and the resulting active Resources are emitted as one Option C
  batch.
- An ordinary series whose identity remains incomplete is omitted with its
  bounded diagnostic while other valid series continue. This Option C
  translation failure does not change the scrape transport result or the value
  of `up`; those continue to describe acquisition and parsing of the scrape.
  Invalid `target_info` likewise contributes no metadata according to the rules
  above without changing scrape success.

### Remote Write Input

- A full-profile input path MUST use the Remote Write 2.0 message and provide
  the input atomic-delivery capability. The sender MUST place exactly one
  complete, pre-established source translation unit in one request. The source
  boundary and its per-identity metadata snapshots MUST exist before Remote
  Write queueing or request construction. Remote Write 1.0 and Remote Write 2.0
  paths without this capability are identity-profile only.
- The receiver MUST associate `target_info` and ordinary series across the
  complete request independently of their order. With Option C producer
  emission enabled, it MUST neither read nor update a cross-request
  `target_info` cache; request-local grouping is permitted. A `target_info`
  series from an earlier or later request cannot contribute metadata to the
  current request.
- Validate the complete request before updating shared state or invoking the
  downstream consumer. Reject ordinary series with incomplete identity and
  malformed or unassociable `target_info`, while translating valid siblings.
  Valid stale or inactive `target_info` is accepted as state but contributes no
  metadata. A valid recognized `target_info` sample is considered written when
  it is successfully consumed by translation even though it is not emitted as
  an OTLP metric. A request containing only valid recognized `target_info` may
  therefore succeed and report those samples as written while emitting no OTLP
  `ResourceMetrics`.
- After the downstream consumer accepts the translated valid data, return a
  success response only if every received sample, histogram, and exemplar was
  accepted. A partial or total Option C rejection returns HTTP `400 Bad
  Request`, is permanent, and MUST NOT be retried. For the Remote Write 2.0
  message, report the exact successfully written sample, histogram, and exemplar
  counts in the required response headers, including zero for each category in
  a wholly rejected request. The deprecated Remote Write 1.0 message follows
  its existing response format without the 2.0 written-count requirement.

## Output Transport and Failure Semantics

### Pull

- A pull round trip ends after a receiving Prometheus scraper ingests the
  exporter's output. That scraper MUST use `honor_labels: true` or relabeling
  that produces exactly the same final `job` and `instance` values on ordinary
  metrics and canonical `target_info`. With the default `honor_labels: false`
  conflict behavior and no equivalent restoration, the scraped identity is
  renamed to `exported_job` and `exported_instance`, so the path conforms to
  neither Option C profile.
- A full-profile exporter MUST expose every ordinary series and canonical
  `target_info` derived from one preserved Option C batch in the same scrape and
  MUST NOT coalesce multiple Option C batches. Unrelated non-Option-C series may
  coexist when they neither collide with ordinary output nor occupy an active
  canonical `target_info` slot.
- Validate the complete Option C snapshot before writing the response. An
  invalid version marker, full-profile metadata conflict, ordinary or semantic
  `target_info` output collision, missing required canonical sample, or hard
  limit fails the entire scrape with a non-success status and no partial metrics
  body. The exporter MUST NOT serve a previous valid snapshot as though it
  represented the rejected current batch.

### Remote Write

- Every full-profile Remote Write path MUST use Remote Write 2.0 because Remote
  Write 1.0 requires label names to match the legacy grammar and therefore
  cannot represent the covered dotted names conformantly. Remote Write 2.0
  support alone is insufficient because the protocol does not define
  transactional delivery.
- A full-profile path additionally requires an Option C atomic-delivery
  capability. The sender MUST place exactly one complete Option C batch,
  including every identity group's ordinary series and canonical `target_info`,
  in one request. Batching, queues, WAL persistence, retries, sharding, and
  concurrent workers MUST neither split that request nor mix it with another
  Option C batch, and retries MUST operate on the complete request. The receiver
  MUST accept or reject the complete request as a unit and, on success, make it
  visible as a unit rather than leave a partially committed Option C batch.
- Validate before adding data to a queue or WAL. An invalid or over-limit batch
  produces no Remote Write request and returns a permanent error; the unchanged
  batch MUST NOT be retried. A full-profile batch that exceeds a hard request
  limit fails instead of being split. Semantic `target_info` slot collisions and
  missing required canonical timestamps are validation failures under the same
  rule.
- Remote Write 1.0, Remote Write 2.0 without the atomic-delivery capability, and
  receivers that may partially commit a request are identity-profile only.

### Direct OTLP Ingestion

- A direct OTLP-to-Prometheus endpoint MUST validate every identity group before
  mutating Prometheus storage. When it accepts at least one group or Resource,
  it MUST report rejected invalid Resources or groups using OTLP partial success
  with the exact `rejected_data_points` count. The client MUST NOT retry that
  partial-success response.
- If no data point in the request is acceptable, return a non-retryable failure:
  gRPC `InvalidArgument` or HTTP `400 Bad Request`. Invalid or unknown version
  markers reject their Resources. Full-profile metadata conflicts and output
  collisions, including semantic `target_info` slot collisions and missing
  required canonical timestamps, reject their complete identity groups.
- A version `"1"` Resource with a malformed reserved pair retains the atomic
  legacy identity fallback defined above and is not rejected. On the producer
  side, incomplete scrape identity retains its series-local failure: omit that
  source series, report its bounded diagnostic, and continue translating other
  valid series.

### Snapshot and Series Lifecycle

- The full profile preserves the canonical `target_info` representation
  generated from each accepted Option C batch. It does not assert that no
  historical `target_info` series for the same reserved pair remains in
  Prometheus storage or that queries cannot observe overlapping old and new
  metadata during a transition.
- Option C does not relax existing staleness requirements. A receiving pull
  scrape retains its ordinary stale-series handling. A Remote Write sender MUST
  continue to emit stale markers whenever discontinuation is detectable under
  the Remote Write specification. Direct OTLP ingestion retains its existing
  series-lifecycle behavior. A marker emitted by that lifecycle tracking to
  retire a previously canonical `target_info` label set is not a second active
  metadata representation and does not collide with the current canonical slot.
- Continuous, query-unambiguous service metadata across changes to a canonical
  `target_info` label set requires lifecycle behavior beyond Option C v1. Such
  behavior may strengthen a path but does not enlarge Option C's guarantee;
  independently applicable protocol requirements remain mandatory.

## Translation Scenarios

The tables below summarize the Option C rules above; those rules remain
authoritative. They do not apply to the earlier Summary of Translation Flows.
Here, `service.*` means any individually present subset of the covered service
attributes, and the legacy path means the complete existing translation. Rows
whose conditions occur together are read together.

### Prometheus to OTLP

Except for the first row, these scenarios assume producer emission is enabled.

| Prometheus input | OTLP Resource attributes | OTLP metric data point attributes and guarantee |
| :---- | :---- | :---- |
| Producer emission disabled | Complete legacy Resource output; no control attributes | Complete legacy point handling; Option C does not apply |
| Producer emission enabled, with complete normalized `job` / `instance` and no associated `target_info` | Active v1 tuple; covered `service.*` is absent | Scrape `job` / `instance` is not repeated; other ordinary metric labels remain point attributes |
| Producer emission enabled, with complete normalized identity and active associated `target_info` containing unambiguous, non-empty `service.*` | Active v1 tuple plus exactly those covered `service.*` values | Same point handling as above; `target_info` metric and sample timing are not represented |
| One producer translation unit produces several active v1 Resources | All Resources are emitted together as one Option C batch | Splitting or coalescing the producer batch leaves identity intact but makes the downstream path ineligible for the full profile |
| Several active associated `target_info` series agree on a covered key | The single agreed value is stored | Presence on one or several source series is not distinguished |
| Several active associated `target_info` series disagree on one covered key | The conflicting key is omitted; other unambiguous covered keys are retained | One bounded diagnostic is reported for the conflicting key and identity pair |
| Active associated `target_info` has an empty covered value | That covered key is absent unless another active series supplies one unambiguous non-empty value | The empty value is outside the guarantee |
| Ordinary metric labels named `service.*` or after any control attribute | They do not populate or overwrite Resource identity, version, or service metadata | They remain ordinary point attributes; only associated `target_info` supplies covered service Resource attributes |
| `target_info` labels named after any control attribute | The producer-generated active v1 tuple remains authoritative; those labels cannot overwrite or supply it | Control-name metadata is ignored; independently valid covered service metadata follows the rule above |
| Associated `target_info` has no samples or its selected state is stale | The active v1 tuple remains authoritative; that series contributes no metadata | Earlier samples are not used as fallback |
| Selected `target_info` state is malformed or has a conflicting greatest-timestamp tie | The active v1 tuple remains authoritative; that series contributes no metadata | One bounded diagnostic is reported; its metadata is outside the guarantee |
| Ordinary series identity remains incomplete after target-context filling | Nothing is emitted for that series | The series fails with a bounded diagnostic; no partial reserved pair is emitted |
| `target_info` identity remains incomplete after target-context filling | It cannot associate or contribute metadata | One bounded diagnostic is reported |
| Recognized `target_info` without supported ordinary metric points | No `ResourceMetrics` is emitted for that identity | `target_info`-only input is consumed and outside the round-trip guarantee |
| Renamed `target_info` not recognized by the producer | It is handled as an ordinary metric | Service-metadata round-tripping does not apply |

Other target metadata retains its existing handling but is outside the
round-trip guarantee.

### Producer Input Transport

| Input scenario | Required result |
| :---- | :---- |
| One scrape transaction contains ordinary series and associated `target_info` | Associate within that scrape and emit its active Resources as one Option C batch; eligible for the full profile when the remaining path requirements hold |
| Remote Write 2.0 carries exactly one complete source translation unit whose boundary and per-identity metadata snapshots were established before transport batching, through a path with the input atomic-delivery capability | Associate all series in the request independent of order and emit one Option C batch; eligible for the full profile when the remaining requirements hold |
| Remote Write request construction defines the purported source unit, combines several pre-established units, splits one unit, independently shards its series, or otherwise lacks the input atomic-delivery capability | Identity-profile only; the request itself is not proof of a coherent source unit, and no `target_info` is associated across requests |
| Remote Write 1.0 carries the input | Identity-profile only; the legacy label grammar cannot preserve the covered dotted names conformantly |
| Option C producer emission is enabled and a cross-request `target_info` cache contains matching identity | Ignore the cached entry and use only the current producer translation unit |
| Invalid input series and valid siblings share a scrape transaction | Omit the invalid series with a bounded diagnostic; translate the valid siblings without changing scrape success or `up` |
| Invalid input series and valid siblings share a Remote Write request | Validate before downstream mutation, accept the valid siblings, and return the input-specific partial-rejection response |
| Every series in a Remote Write request is rejected | Emit no Option C batch and return HTTP `400 Bad Request`; Remote Write 2.0 written counts are zero |
| A Remote Write request contains only valid recognized `target_info` | Emit no `ResourceMetrics`; accept the request and count its samples as written |

### OTLP to Prometheus

| OTLP input | Prometheus `job` / `instance` | Other labels, metadata, and guarantee |
| :---- | :---- | :---- |
| Consumer recognition disabled, with any control-attribute combination | Complete legacy path | No control attribute is reserved or suppressed; Option C does not apply |
| Version marker absent, whether the pair is absent, partial, or complete | Complete legacy path | No control attribute is reserved or suppressed; existing metadata, promotion, collision, and `keep_identifying_resource_attributes` behavior applies |
| Version marker present but empty, non-string, or unknown | No output for the Resource | Fail closed and report one bounded diagnostic; do not interpret the pair or emit any associated point |
| Version marker is `"1"`, but the pair is partial, empty, or non-string | Both labels use the complete legacy identity path | Report one bounded diagnostic, suppress all three control attributes by default, and never combine a reserved value with a legacy value |
| Active v1 tuple without additional Resource metadata | Reserved pair | No service metadata is synthesized and control attributes are suppressed by default; the full profile emits one active canonical `target_info` label set, while other output modes need not |
| Active v1 tuple with non-empty string `service.*` | Reserved pair | When enabled, include the covered values in canonical `target_info`, subject to the identity-group merge rules and regardless of `keep_identifying_resource_attributes` |
| Active v1 tuple with empty or non-string `service.*` | Reserved pair | Existing translation behavior applies; those service values are outside the guarantee |
| Active v1 tuple with other non-service Resource metadata | Reserved pair | Existing selection and translation rules produce candidates for the canonical merge; this metadata remains outside the round-trip guarantee |
| Several active v1 Resources share a reserved pair and candidate metadata has at most one value per final label name | Reserved pair | Merge the candidates and emit at most one active canonical `target_info` label set for the slot; union, deduplicate, and order their output-specific sample schedules |
| An identity-profile output coalesces Option C batches whose identity groups map to the same canonical slot | Reserved pair | Merge all canonical candidates using the merge-and-omit rule and emit at most one active label set; the coalesced output remains ineligible for the full profile |
| Candidate non-covered metadata supplies conflicting values for one final label name | Reserved pair | Omit that label from the canonical `target_info` and report one bounded diagnostic; the conflict does not affect either profile's covered guarantee |
| Covered `service.*` differs in presence or value within an identity group | Reserved pair | An identity-profile output omits the conflicting label; a full-profile output fails validation without silently downgrading |
| Active v1 tuple and conflicting point-level or exporter-added `job` / `instance` | Reserved pair | The conflicting identity is overwritten; other point attributes retain existing handling |
| Point attributes named after any control attribute, with or without an active v1 tuple | Reserved pair when active; otherwise complete legacy path | Same-named point attributes remain ordinary translated labels and do not activate Option C |
| `target_info` generation disabled or renamed | Reserved pair when active; otherwise complete legacy path | Active identity remains covered, but the path is identity-profile only |
| An active control attribute explicitly promoted or converted to an ordinary label | Reserved pair | Emit the promoted attribute under its translated name on ordinary metrics, not as `target_info` metadata; the resulting label set and collisions are outside the full-round-trip guarantee |
| Active v1 tuple and `promote_all_resource_attributes` with all control attributes ignored | Reserved pair | Default Option C control handling; other Resource attributes retain existing promotion behavior |
| Covered `service.*` uses a UTF-8-preserving, injective label mapping | Reserved pair | Subject to the other full-profile requirements, its presence and value on canonical `target_info` are covered |
| Covered `service.*` is renamed or collides after label translation | Reserved pair | The affected service key is outside the guarantee; the path is identity-profile only |
| Distinct input points map to the same final Prometheus series and timestamp or incompatible metric-family definitions | Reserved pair for any emitted identity-profile point | Existing collision handling applies in the identity profile; full-profile validation fails the affected identity group, including for equal duplicate values |
| Ordinary or legacy output maps to the same final metric name and `job` / `instance` pair as generated canonical `target_info` | Reserved pair | Never emit both: identity profile retains canonical `target_info`, drops all competitors, and reports one bounded diagnostic for the slot; full profile fails the affected identity group under the output-specific scope |
| Several Resources contribute the same canonical `target_info` timestamp | Reserved pair | Emit one generated sample at that timestamp; this deduplication is not an ordinary-point collision |
| A timestamp-carrying output has no usable canonical `target_info` timestamp | Reserved pair | Identity profile omits canonical `target_info`; full profile fails validation for the affected identity group |
| A later output operation changes the canonical covered `service.*` for a reserved pair | Reserved pair | The full profile covers the new operation's canonical snapshot; historical-series retirement and query-time uniqueness are outside Option C v1 |

### Full-round-trip Transport

| Export scenario | Required result |
| :---- | :---- |
| One preserved Option C batch, with all ordinary series and canonical `target_info` exposed in one pull scrape and ingested with `honor_labels: true` or exact equivalent | Eligible for the full profile |
| Several active v1 Resources share a reserved pair and have identical presence and values for every covered service attribute | Treat the Resources as one coherent identity group, emit one active canonical `target_info` label set, and union and deduplicate its sample schedule; eligible for the full profile |
| Resources in an identity group differ in the presence or value of any covered service attribute | Full-profile validation fails for the group with one bounded diagnostic; the output-specific failure scope applies and no silent downgrade occurs |
| Ordinary or legacy output occupies an active canonical `target_info` slot | Full-profile validation fails for the affected identity group; the output-specific failure scope applies and the two representations are never emitted together |
| Pull uses default `honor_labels: false` without exactly restoring the exposed identity | Neither profile; the receiving scrape replaces `job` and `instance` |
| Pull coalesces several Option C batches into one scrape or splits one across snapshots | Identity-profile only; no service-metadata guarantee applies |
| Remote Write 1.0 carries the output | Identity-profile only because covered dotted label names cannot be preserved conformantly |
| Remote Write 2.0 sends one complete Option C batch and the sender, WAL, retry path, and receiver provide atomic delivery | Eligible for the full profile |
| Remote Write 2.0 is used without atomic delivery, or a batch is split, mixed, or partially committed | Identity-profile only; Remote Write 2.0 alone does not provide the full profile |
| A full-profile Option C batch exceeds a hard request limit | Fail the batch permanently; do not split or silently downgrade it |
| Direct OTLP ingestion rejects some invalid Resources or groups but accepts others | Return OTLP partial success with the exact rejected-point count; accepted groups retain their applicable profile |
| Direct OTLP ingestion rejects every data point | Return non-retryable gRPC `InvalidArgument` or HTTP `400 Bad Request` |
| Pull or Remote Write full-profile validation fails | Emit no partial scrape or request; pull returns a non-success response and Remote Write returns a permanent pre-queue error |
| A canonical `target_info` label set changes between output operations | Preserve each operation's snapshot; continue to apply the output protocol's existing staleness rules without claiming cross-operation query uniqueness |
| A Remote Write sender can detect that an earlier generated `target_info` series is discontinued | Emit the required stale marker independently of Option C conformance; a verified retirement marker is lifecycle output and may coexist with the current active canonical series |

## Rollout Compatibility

The attribute names and behavior must be standardized before producers emit the
marker or consumers recognize it. Consumer support is backward compatible only
while recognition is disabled or the marker is absent. Pair attributes without
the marker continue to receive complete legacy handling, but pre-existing use
of `prometheus.scrape.identity.version` can activate or fail Option C after
recognition is enabled and MUST be inventoried first. Producer emission and
consumer recognition MUST both initially default to disabled.

| Producer | Producer emission | Consumer recognition | Result |
| :---- | :---- | :---- | :---- |
| Any existing producer | Not applicable | Disabled | Complete legacy behavior for every control-shaped attribute |
| Existing producer without the version marker | Not applicable | Enabled | Complete legacy behavior, including legacy handling of either pair attribute |
| Existing producer using the version-marker name | Not applicable | Enabled | Potential collision: migrate or isolate before enabling recognition; the marker otherwise activates or fails Option C according to its value |
| Option C v1 producer | Disabled | Disabled or enabled | Complete legacy producer behavior; no control attributes are emitted |
| Option C v1 producer | Enabled | Enabled on an identity-profile path | Exact scrape identity; no service-metadata guarantee |
| Option C v1 producer | Enabled | Enabled on a full-round-trip-profile path | Exact scrape identity and covered service metadata |
| Option C v1 producer | Enabled | Disabled or unsupported | Unsupported; scrape identity may be lost, replaced, or exposed only as unrelated metadata |
| Producer with an empty, non-string, or unknown version marker | Not applicable | Enabled | Resource fails closed according to the output-specific failure semantics |

Implementations MUST first standardize the names and behavior, then deploy
consumers with recognition disabled. Operators MUST inventory every input that
uses `prometheus.scrape.identity.version` and migrate or isolate collisions
before enabling recognition on the intended endpoints. They MUST then verify
recognition and tuple preservation across every fan-out branch before enabling
producer emission. The operator may claim the full profile only when every hop
also preserves the Option C batch and meets the output-specific metadata,
label, and atomic-delivery requirements. For Remote Write input, the operator
MUST additionally verify the sender-side input atomic-delivery capability and
that the source-unit boundary and coherent per-identity metadata snapshots are
established before transport batching, and that the Option C producer bypasses
cross-request `target_info` state. A single apparently complete request is not
proof of that capability or boundary. Existing default configurations that
translate dotted names to underscores are identity-profile only. Defining
another marker value or changing either gate to default-on is outside this
proposal and requires a separate compatibility decision.

## Required Specification Changes

Adopting Option C requires normative specification changes; this document does
not itself override the existing Prometheus compatibility specification.

- Register `prometheus.scrape.identity.version` as a string Resource control
  attribute, with `"1"` as the only version defined here. Register
  `prometheus.job` and `prometheus.instance` as string Resource control
  attributes whose v1 values must be non-empty.
- Define Prometheus-to-OTLP grouping and enabled producer emission of the active
  v1 tuple, including the atomic emission and failure rules above.
- Define scrape and Remote Write producer-input conformance, including the
  pre-transport source-unit boundary, per-identity coherent metadata snapshots,
  Remote Write 2.0 input atomic-delivery capability, order-independent
  same-request association, mandatory bypass of cross-request `target_info`
  state, series-local rejection, and protocol-specific response semantics.
- Define consumer dispatch by the Resource version marker, including complete
  legacy handling when recognition is disabled or the marker is absent,
  fail-closed handling for an invalid or unknown marker, and atomic legacy
  identity fallback for a malformed v1 pair.
- Exempt active control attributes from generic Resource-attribute copying to
  `target_info`. A generated `target_info` does not contain them by default.
- Require aggregated Prometheus exporters to check for an active v1 tuple before
  applying unnamespaced or `service.*` identity fallbacks.
- Require at most one active canonical `target_info` label set per final metric
  name and `job` / `instance` pair in each output operation, including
  cross-batch identity-profile merging and the merge-and-omit rules for
  conflicting metadata. Reserve that slot against ordinary and legacy series,
  with canonical-wins identity-profile handling and full-profile failure. In the
  full profile, require it to include every present covered service attribute
  with a non-empty string value, regardless of
  `keep_identifying_resource_attributes`, and require exactly one such series
  even when all covered service attributes are absent.
- Define output-specific canonical sample schedules, union, ordering, and
  deduplication across Resources, failure when a full-profile schedule is empty,
  and the lifecycle-only exception for verified retirement stale markers.
- Define the producer-owned Option C batch boundary, full-profile preservation
  across intermediaries, final output-collision validation, pull label-conflict
  handling, Remote Write 2.0 plus atomic-delivery requirements, and the
  output-specific permanent and partial failure semantics above.
- Define the full profile as a per-batch snapshot guarantee. Preserve existing
  pull, Remote Write, and direct-ingestion series-lifecycle rules, including
  independently applicable Remote Write stale-marker requirements and the
  distinction between retirement markers and active metadata, without adding a
  query-time uniqueness guarantee across output operations.
- Preserve existing `target_info` metric type and sample-value handling,
  ordinary metadata behavior, and explicit Resource-attribute promotion
  semantics except where the rules above explicitly override them.

Until those changes are adopted, the existing specification remains
authoritative and an implementation cannot claim Option C conformance merely by
following this proposal.

## Round-trip Guarantee and Limits

For supported ordinary metric points carried by an active v1 tuple, an
identity-profile path preserves the exact normalized scrape `job` and
`instance`, provided the final output point does not collide and, for pull, the
receiving scrape preserves those labels. A full-round-trip-profile path
additionally preserves the individual presence and non-empty string value of
each covered service attribute obtained from valid associated `target_info`.
The full guarantee applies only to coherent identity groups in a preserved
Option C batch and only when every path requirement in the Conformance Profiles
and input and output transport sections is met. It is evaluated for the output
generated and accepted from that batch; it does not assert the absence of
historical `target_info` series or unambiguous query results across successive
operations whose metadata differs.

Neither profile reproduces the source `target_info` time series or preserves its
one-to-one series or sample presence, timestamps, or cadence; a consumer may
generate new `target_info` only to represent Resource metadata. Neither profile
covers unsupported metric points, incomplete or malformed scrape identity,
unknown or invalid version markers, additional receiver- or exporter-added
enrichment or external labels, or semantics-changing processors that alter the
covered identity or metadata. It also excludes points subject to ordinary or
semantic `target_info` output collisions, points dropped because they compete
with a canonical slot, incompatible metric-family definitions, and pull scrapes
that replace the exported `job` and `instance` values.

The full guarantee additionally excludes `target_info`-only input, other target
metadata, stale, malformed, or conflicting target metadata, empty or non-string
service values, identity groups with conflicting service-attribute presence or
values, disabled or renamed `target_info`, incoherent or split identity-group
or Option C batch transport, a source unit whose boundary or per-identity
metadata snapshot was not established before transport batching, Remote Write
input without the input atomic-delivery capability, cross-request target
metadata, a missing required canonical sample timestamp, batches that fail a
hard request limit, lossy or colliding label-name translation for the affected
service key, non-atomic Remote Write output, and configurations that explicitly
promote a control attribute. It also excludes cross-operation `target_info`
retirement and query-time uniqueness during metadata changes, without relaxing
independently applicable staleness requirements. A protocol-required marker
that retires previously canonical `target_info` remains lifecycle output rather
than an active metadata representation. Explicit promotion does not change
active-tuple identity handling, but the resulting ordinary label set is outside
the full guarantee. The control tuple is not copied to generated `target_info`
by default. Existing default configurations that translate dotted names to
underscores are identity-profile only. A full-profile validation failure follows
the output-specific failure behavior and never silently downgrades the rejected
batch.
