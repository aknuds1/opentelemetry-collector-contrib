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
native OTLP data, which is already handled by Prometheus'
`keep_identifying_resource_attributes` option.

## Contract at a Glance

Option C producers store the normalized Prometheus scrape identity as the
Resource tuple:

- `prometheus.scrape.identity.version = "1"`
- `prometheus.job = <normalized job>`
- `prometheus.instance = <normalized instance>`

The marker makes the namespaced attributes translation control rather than
ordinary Resource metadata. Option C consumers use the pair atomically as the
Prometheus `job` and `instance` labels. The pair never supplies or defaults
`service.name`, `service.namespace`, or `service.instance.id`.

| Direction | Scenario | Required result |
| :---- | :---- | :---- |
| Prometheus to OTLP | Complete normalized `job` and `instance`, no target metadata | Store the active v1 tuple; do not synthesize `service.*` |
| Prometheus to OTLP | Complete identity and valid associated target metadata | Store the active v1 tuple and target metadata as Resource attributes; consume the target-info series |
| Prometheus to OTLP | A service-looking label appears only on an ordinary metric | Keep it as an ordinary data point attribute; it is not covered Resource metadata |
| Prometheus to OTLP | Ordinary series has incomplete normalized identity | Omit that series with one bounded diagnostic |
| Prometheus to OTLP | Target metadata is malformed, conflicting, or cannot associate | Exclude the invalid metadata or conflicting key under the transport-specific failure scope; valid scrape siblings continue |
| Prometheus to OTLP | Producer emission is disabled | Preserve complete legacy translation |
| OTLP to Prometheus | Consumer recognition is disabled | Preserve complete legacy translation, regardless of the control attributes |
| OTLP to Prometheus | Marker is absent | Preserve complete legacy translation, even if one or both pair attributes exist |
| OTLP to Prometheus | Markerless Resource contains only legacy `service.*`, bare `job`, or both | Preserve existing fallback, promotion, and collision behavior |
| OTLP to Prometheus | Marker and pair form a valid active v1 tuple | Use the pair atomically as authoritative `job` and `instance` on every associated output point |
| OTLP to Prometheus | Marker is present but the marker or pair is invalid | Fail the Resource closed; do not fall back to legacy identity |
| OTLP to Prometheus | Active tuple has covered service attributes | When canonical target-info generation is enabled, carry those attributes on the generated `target_info` series |
| OTLP to Prometheus | Active tuple conflicts with point-level or exporter-added identity | The active tuple wins atomically |
| OTLP to Prometheus | A control attribute is explicitly promoted | Apply existing promotion behavior; the resulting ordinary label set is outside the round-trip guarantee |
| End to end | Pull output preserves exported identity with `honor_labels: true` or exact equivalent | Eligible for the identity profile only |
| End to end | One Option C batch is preserved in one atomically delivered Remote Write 2.0 request or direct-ingestion transaction | Eligible for the full profile when all other full-profile requirements hold |
| End to end | A batch is split, coalesced, mixed, partially committed, or exposed through ordinary pull state | Identity-profile only; no service-metadata round-trip guarantee |

## Interoperability Contract

- A **producer** is a Prometheus or OpenMetrics to OTLP translator that emits
  Option C attributes. A **consumer** is an OTLP to Prometheus translator that
  recognizes them and produces Prometheus identity, such as Prometheus OTLP
  ingestion or an aggregated Prometheus exporter.
- The three Resource **control attributes** are
  `prometheus.scrape.identity.version`, `prometheus.job`, and
  `prometheus.instance`. The latter two form the **reserved pair**.
- Consumer recognition MUST be gated behind an implementation-specific option
  that defaults to disabled and can be scoped to an input endpoint or pipeline.
  Producer emission MUST have a separate option that also defaults to disabled.
- An **active v1 tuple** has the version marker set to the string `"1"` and
  both members of the reserved pair present as non-empty strings.
- The **covered service attributes** are `service.name`,
  `service.namespace`, and `service.instance.id`. The full-profile guarantee
  covers the presence and value of one of these attributes only when a producer
  obtained its non-empty string value from valid associated target metadata.
- A **source translation unit** is one original scrape transaction or one
  application transaction whose complete contents and boundary exist before
  transport batching, queueing, sharding, WAL persistence, retries, or request
  assembly.
- An **Option C batch** contains all active v1 Resources produced from one
  source translation unit. The batch boundary is an end-to-end capability, not
  a new OTLP field.
- An **identity group** contains the active v1 Resources in one Option C batch
  that have the same reserved pair.
- A **canonical target-info slot** is the representation-independent output
  position for one final `job` and `instance` pair in one output operation.
  Native Info and Gauge fallback output occupy the same slot.
- A **configured output path** is the endpoint and fixed chain of translators,
  processors, queues, encodings, and receivers through which output travels.
- A **canonical target-info representation** is either the semantic `target`
  Info family or the `target_info` Gauge fallback. In standard text encoding,
  the Info family has the concrete sample name `target_info`; the Gauge
  fallback also has the concrete series name `target_info`. A flattened format
  such as Remote Write 2.0 represents the native form as concrete series
  `target_info` with Info metadata because it does not carry a separate family
  name.
- An **output operation** is one complete pull response, one logical Remote
  Write request including its retries, or one direct-ingestion transaction.

For active v1 Resources, the control attributes are consumed as translation
control and identity. By default, they are not emitted as target metadata or as
ordinary metric labels. Source labels or preexisting Resource attributes with
the same names cannot activate Option C or overwrite producer-generated
control values.

## Prometheus to OTLP

Before applying Option C, complete ordinary protocol negotiation, decoding,
structural validation, source relabeling, scrape-target identity filling, and
label validation. Option C does not turn protocol-invalid input into a
semantic, entity-local failure.

### Identity and Resource Construction

- Normalize `job` and `instance` using the existing scrape or Remote Write
  rules. An ordinary series participates in Option C only when both final
  values are non-empty.
- Group supported ordinary points by the exact normalized pair. Store the
  active v1 tuple on each resulting Resource and do not also store source
  `job` or `instance` as data point attributes.
- Emit all active v1 Resources from one source translation unit together as one
  Option C batch. Splitting or coalescing that batch preserves the identity
  profile but makes the downstream path ineligible for the full profile.
- A service-looking label on an ordinary series remains a data point attribute.
  Only valid associated target metadata supplies covered service Resource
  attributes.
- Never default any `service.*` attribute from `job` or `instance`.

### Target Metadata Recognition

Recognition is representation-aware. It uses parser-provided family and type
evidence when available and exact final series names otherwise. It never
reconstructs a target-info family by stripping a type-specific suffix.
Family or type evidence participates only when it still describes the final
exact scalar representation after relabeling; stale evidence cannot reclassify
a renamed series.

| Final input evidence after relabeling | Classification |
| :---- | :---- |
| Family-aware input identifies semantic family `target`, Info type, and a scalar Info point whose concrete text/flat sample is `target_info` | Accepted native target metadata |
| Exact scalar `target_info` with Gauge, Info, unknown, absent, or unspecified type | Accepted fallback or flattened native target metadata |
| Remote Write 2.0 fragment has exact `__name__="target_info"` with Gauge, Info, or unspecified metadata and scalar samples | Compatible target-metadata evidence |
| Exact `target_info` has another asserted type or a histogram shape | Invalid reserved input |
| Family-aware `target` Info evidence has an incompatible sample shape or assertion | Invalid reserved input |
| Flat series has exact concrete name `target`, with or without an Info assertion | Ordinary input; a noncanonical concrete name does not imply the semantic `target` family |
| `target_info_total`, `target_info_bucket`, `target_info_sum`, `target_info_count`, or another suffix-looking name | Ordinary input; type-specific suffix removal does not reserve it |
| Any other exact or suffix-looking name | Ordinary input |

For Remote Write 2.0, classify every `TimeSeries` fragment before combining
messages with the same complete final labels into one logical series. Retain
each fragment's type, shape, samples, histograms, and exemplars. For an exact
`target_info` candidate, Gauge, Info, and no-assertion scalar fragments are
compatible. Any incompatible asserted type or shape invalidates the complete
same-label logical series. A fragment named `target_info_total` remains
ordinary even when it asserts Counter type.

HELP, UNIT, and optional start timestamps do not participate in target-metadata
recognition after their ordinary protocol validation succeeds.

### Association and Selected State

- Associate target metadata only with ordinary series having the same exact
  normalized pair in the same source translation unit. An incomplete or
  unassociable target identity produces one bounded diagnostic and supplies no
  metadata.
- For each associated target-info series, select its greatest-timestamp scalar
  sample. Equal greatest timestamps represent one state only when all samples
  are stale, or all are non-stale with value `1`.
- A selected stale state is inactive and supplies no Resource metadata. A
  selected non-stale state is valid only when its value is `1`.
- Remove the concrete metric name and the identity labels from the selected
  target-info labels. Convert the remaining labels to Resource attributes using
  existing name and value rules, except that the control-attribute names remain
  reserved and cannot be supplied by target metadata.
- When several valid associated target-info series supply the same Resource
  key, retain the value if all suppliers agree. If they supply different
  values, omit that key and report one bounded diagnostic. Valid sibling keys
  and ordinary points continue.
- Consume recognized target-info scalar samples as metadata; do not emit them
  as OTLP metrics. Their original presence, timestamps, cadence, HELP, UNIT,
  start timestamps, and exemplars are outside the round-trip guarantee.
- A translation unit containing only consumed target-info input emits no empty
  `ResourceMetrics`.

When producer emission is disabled, preserve complete existing input, cache,
and response behavior.

## OTLP to Prometheus

### Consumer Dispatch

Consumer dispatch is exhaustive and atomic for the reserved pair:

| Recognition state and marker | Required behavior |
| :---- | :---- |
| Recognition disabled | Apply complete legacy identity, metadata, promotion, and collision behavior to every Resource |
| Recognition enabled, marker absent | Apply complete legacy behavior; do not reserve or suppress either pair attribute |
| Marker is exactly string `"1"` and both pair members are non-empty strings | Activate Option C and use the pair atomically |
| Marker is present but empty, non-string, unknown, or paired with a partial, empty, or non-string reserved pair | Fail the Resource closed with one bounded diagnostic; emit none of its points and do not use legacy fallback |

Markerless legacy output remains subject to final collision arbitration when it
shares an output operation with active v1 output, but its Resource translation
otherwise remains completely legacy.

### Active Tuple and Metadata Merge

- Use `prometheus.job` and `prometheus.instance` atomically as `job` and
  `instance` on every ordinary point and generated target-info representation
  for the Resource. They override conflicting point-level, exporter-added, or
  service-derived identity.
- Group active v1 Resources in the received Option C batch by reserved pair.
  Compute target-info candidates using existing Resource-attribute selection,
  conversion, final label naming, and collision rules. Exclude the control
  attributes and add the reserved pair separately as `job` and `instance`.
- When target-info generation is enabled, include every present covered service
  attribute with a non-empty string value as a candidate regardless of
  `keep_identifying_resource_attributes`.
- For each final metadata label other than `job` and `instance`, include one
  value when all Resources that supply the label agree. In the identity
  profile, omit a conflicting label and report one bounded diagnostic; absence
  on another Resource is not a conflict.
- In the full profile, every Resource in an identity group MUST have identical
  presence and, when present, the same non-empty string value for each covered
  service attribute. A difference fails the complete Option C batch and output
  operation. Non-covered metadata continues to use merge-and-omit.
- If an identity-profile output coalesces several Option C batches, merge groups
  having the same pair with the same merge-and-omit rule. Coalescing is never
  full-profile eligible.

### Canonical Target-Info Output

Each configured output path MUST pin one semantic representation:

- Use the native `target` Info family when the entire configured path preserves
  Info semantics.
- Otherwise use the `target_info` Gauge fallback with value `1`.
- Both representations use the concrete Prometheus series name `target_info`
  and are semantically equivalent for Option C. Both may satisfy the full
  profile when every other requirement is met.
- In a flat format, concrete `target_info` with Info metadata is the encoding
  of the native semantic `target` family, not a separate semantic Info family
  named `target_info`.
- Never emit both representations. A family-aware output also MUST NOT emit a
  semantic Info family named `target_info`, which would have the concrete
  sample name `target_info_info`. Representation selection MUST NOT vary by
  output operation.
- A pull endpoint that permits formats without Info support MUST pin the Gauge
  fallback for all responses or reject negotiation of incompatible formats.

Generate at most one active canonical target-info label set per reserved pair
in one output operation. The full profile generates it even when the group has
no metadata beyond `job` and `instance`. Outside the full profile, an active
tuple alone does not require target-info output. The control attributes are not
included by default.

Use the existing output-specific sample schedule:

- Pull exposes one sample with value `1` and no explicit timestamp.
- Remote Write uses each contributing Resource's greatest supported ordinary
  point timestamp, then unions, deduplicates, and orders the timestamps for the
  identity group.
- Direct ingestion uses its existing target-info schedule from the earliest to
  latest supported ordinary-point timestamps at half the configured or default
  lookback-delta interval.

If a timestamp-carrying output has no usable target-info timestamp, the
identity profile omits canonical target-info output; the full profile rejects
the complete Option C batch and output operation.

### Final Validation and Collisions

- Before visible output or storage mutation, gather every ordinary, legacy, and
  canonical candidate in the complete output operation after final namespace,
  rename, label, identity, and type-specific naming.
- Repeat canonical merging, sample scheduling, slot reservation, collision
  validation, and full-profile validation at any later layer that changes the
  operation's composition.
- If a final layer cannot repeat that processing, an identity-profile path
  omits generated target-info output and remains identity-only. An asserted
  full-profile operation fails before visible mutation.
- Reserve the canonical target-info slot against another exact final
  `target_info` representation for the same pair. A suffix-looking ordinary
  metric such as `target_info_total` does not occupy this semantic slot.
- Independently apply existing final-series and metric-family definition
  validation. An ordinary suffix-looking metric that creates a real collision
  in the selected output encoding receives ordinary collision handling; the
  identity profile does not drop it merely because of its name, and the full
  profile rejects the complete Option C batch and output operation on an actual
  collision.
- If an exact ordinary, legacy, or markerless target-info candidate occupies the
  canonical slot, the identity profile retains canonical output, omits the
  competitor, and reports one bounded diagnostic. The full profile rejects the
  complete Option C batch and output operation.
- A stale marker is exempt from active-slot collision only when existing
  lifecycle tracking emits it to retire a previously generated canonical label
  set. Arbitrary stale ordinary or legacy input receives no exemption.

Settings that disable target-info generation remain authoritative. They do not
affect active tuple identity, but they remove the service-metadata guarantee.
Namespaced or renamed target-info output retains its configured representation
and concrete-name collision behavior but is noncanonical and identity-profile
only.

Resource-to-ordinary-label conversion remains orthogonal, including
`promote_resource_attributes`, `promote_all_resource_attributes`, and
equivalent settings. A control attribute may be explicitly promoted under the
existing include, ignore, conversion, and collision rules. Such promotion does
not change active tuple identity, but the resulting ordinary label set is
outside both profiles' round-trip guarantees.

Exact covered service-attribute round-tripping requires an injective,
UTF-8-preserving final label-name mapping. A renamed or colliding covered key is
outside the guarantee; reserved scrape identity remains covered.

## Conformance Profiles

Conformance is a property of an end-to-end configured path, not merely a
component binary.

### Identity Profile

An identity-profile path has enabled Option C producers and consumers and
intermediaries that preserve the active v1 tuple. It guarantees the exact
normalized `job` and `instance` values on otherwise supported, noncolliding
output points.

For pull, the receiving scrape is part of the path and MUST preserve those
labels with `honor_labels: true` or an exact equivalent. A scrape that replaces
them with target labels conforms to neither profile.

### Full Round-Trip Profile

A full-profile path additionally preserves the individual presence and
non-empty string value of every covered service attribute obtained from valid
associated target metadata. It requires:

- one complete Option C batch and no split or coalescing;
- canonical target-info generation using the configured path's pinned Info or
  Gauge representation;
- an injective UTF-8-preserving label mapping for covered dotted names;
- identical covered-attribute presence and values within each identity group;
- final operation-wide composition knowledge and collision validation;
- a usable canonical sample schedule;
- no explicitly promoted control attributes;
- no processor that changes covered identity or metadata; and
- an atomic Remote Write 2.0 or direct-ingestion output capability.

A path whose producer input is Remote Write also requires the corresponding
input atomic-delivery capability.

Unrelated non-Option-C data may share an output operation only when final
operation-wide validation proves that it neither joins the Option C batch nor
collides with its ordinary or canonical output.

The full profile is a per-batch snapshot guarantee. Remote Write 2.0 wire
support alone is insufficient because that protocol permits partial writes and
does not define transactionality. A static configuration lacking any required
capability may claim only the identity profile.

An asserted full-profile path MUST NOT silently downgrade dynamically invalid
data. Any full-profile validation failure rejects the complete Option C batch
and output operation before externally visible mutation.

## Transport and Failure Semantics

### Producer Input

For all producer inputs, ordinary content negotiation, decoding, structural
validation, and protocol-specific rejection happen before Option C
classification.

#### Scrape

- A scrape can originate an Option C batch that remains eligible for the full
  profile when ordinary series and associated target metadata come from the
  same scrape transaction and the producer emits all active Resources together.
- An incomplete ordinary identity omits only that series with one bounded
  diagnostic and does not alter scrape success or `up`.
- Invalid reserved target metadata omits that metadata series, and conflicting
  valid metadata omits only the conflicting key. Valid ordinary siblings
  continue. These semantic failures do not alter scrape success or `up`.

#### Remote Write Input

- Remote Write 1.0 and Remote Write 2.0 without an explicit input
  atomic-delivery capability are identity-profile only.
- A full-profile Remote Write input MUST use 2.0 and contain exactly one
  complete, pre-established source translation unit in one request. Request
  assembly, queues, WALs, sharding, and retries cannot establish a missing
  source boundary.
- Associate target metadata across the complete request independently of
  series order. Producer emission MUST NOT read or update a cross-request
  target-info cache.
- For Remote Write 2.0, classify fragments before same-label grouping as
  specified above. Count every wire sample, histogram, and exemplar once;
  logical grouping and selected-state evaluation do not deduplicate response
  counts.
- A recognized valid scalar target-info sample counts as written when consumed
  as metadata even though no OTLP metric is emitted. Invalid attached exemplars
  are rejected independently.
- Validate before shared-state mutation or downstream consumption. After the
  downstream consumer accepts valid data, return success only when every wire
  entity was accepted. An Option C partial or total rejection returns permanent
  HTTP `400 Bad Request`.
- Remote Write 2.0 responses report exact successfully written sample,
  histogram, and exemplar counts in the required headers. A wholly rejected
  request reports zero. Remote Write 1.0 retains its existing response format.

### Consumer Output

#### Pull

- Pull output is never full-profile capable because exporter accumulation and
  scrape timing do not preserve a producer batch snapshot.
- It qualifies for the identity profile only when the receiving scrape
  preserves the exposed `job` and `instance` values.
- Pull output may expose canonical current-state target metadata, but Option C
  makes no source-batch service-metadata round-trip guarantee for it.
- Ordinary output validation and existing scrape response behavior remain
  unchanged; Option C defines no transactional pull queue or full-profile
  non-success response.

#### Remote Write

- Remote Write 1.0 is at most identity-profile capable.
- A full-profile Remote Write 2.0 path places exactly one complete Option C
  batch, including ordinary and canonical target-info series, into one request.
  The sender validates before queue or WAL insertion and fails an over-limit
  batch permanently instead of splitting it.
- Queues, WAL persistence, retries, sharding, and concurrent workers preserve
  the complete request. Retries use the same logical output operation.
- The receiver accepts or rejects the complete request and makes a successful
  request visible atomically. The standard Remote Write partial-write behavior
  is insufficient for this optional Option C capability.
- Either pinned canonical representation may be full-profile eligible when
  Remote Write 2.0 preserves its metadata and the covered dotted label names.

#### Direct OTLP Ingestion

- An identity-profile endpoint may retain existing Resource- or group-scoped
  OTLP partial success and MUST report the exact `rejected_data_points` count.
  Invalid marked Resources are rejected rather than translated through legacy
  identity.
- A full-profile endpoint validates the entire Option C batch before storage
  mutation and accepts or rejects it as one transaction. It MUST NOT return
  partial success for an asserted full-profile batch.
- A completely rejected identity-profile request, or any rejected
  full-profile batch, returns non-retryable gRPC `InvalidArgument` or HTTP
  `400 Bad Request`.

### Snapshot and Series Lifecycle

Option C does not relax existing staleness rules. Pull scrapers and Remote Write
senders continue their existing series-discontinuation behavior. A verified
stale marker retiring a prior canonical label set is lifecycle output, not a
second active representation.

Both pinned semantic representations have concrete series name `target_info`.
Changing the pinned representation is an explicit configuration and
metric-metadata compatibility event, but it does not by itself create a second
series or require retirement when the final label set is unchanged. Changes to
the label set continue to require the output protocol's normal lifecycle
handling. Cross-operation query-time uniqueness during metadata changes remains
outside Option C.

## Rollout Compatibility

The control names and behavior must be standardized before producers emit the
marker or consumers recognize it. The independent gates support a consumer-
first rollout:

| Producer emission | Consumer recognition | Behavior |
| :---- | :---- | :---- |
| Disabled | Disabled | Complete legacy behavior |
| Disabled | Enabled | Markerless Resources receive complete legacy behavior |
| Enabled | Disabled | Control attributes receive legacy metadata, promotion, and collision handling; no identity override |
| Enabled | Enabled | Valid tuples use Option C; malformed marked Resources fail closed |

Before enabling consumer recognition, operators MUST inventory existing use of
all three control names, marker collisions, final `job` and `instance`
collisions at fan-in points, explicit Resource promotion, and processors that
alter the tuple or covered service metadata.

Before enabling the full profile, operators MUST verify the source translation
unit and Option C batch boundary, final label mapping, configured canonical
representation, final composition validation, request limits, queue and retry
behavior, and receiver atomicity. A single apparently complete request does not
prove those capabilities.

Downstream family- or type-aware translators must accept the configured native
Info or Gauge fallback representation. PromQL consumers continue to select the
concrete `target_info` series for either standard representation; they MUST NOT
be migrated to query both `target` and `target_info`. Simultaneous canonical
emission remains invalid because it would duplicate concrete output.

The configured representation is fixed for a path. Changing it requires an
explicit compatibility review for consumers of metric family and type metadata.
Existing default mappings that translate covered dotted names to underscores
remain identity-profile only.

Defining another marker value, making either gate default-on, or adding a
different concrete canonical series name is outside Option C v1 and requires a
separate compatibility decision.

## Required Specification Changes

Adopting Option C requires normative Prometheus/OpenMetrics compatibility
specification changes:

- Define the three control Resource attributes, independent default-disabled
  producer and consumer gates, and the exhaustive marker dispatch table.
- Define producer ownership of the active tuple, exact normalized identity,
  target metadata association, selected-state rules, Resource grouping, and
  Option C batch boundary.
- Define target metadata using semantic family, concrete series name, type, and
  scalar shape. Explicitly forbid suffix-removal classification and preserve
  ordinary `target_info_total`-like metrics.
- Define active-tuple consumer precedence, operation-wide identity grouping,
  covered-metadata conflict behavior, default control-attribute consumption,
  explicit promotion behavior, and the
  `keep_identifying_resource_attributes` override.
- Define one pinned canonical semantic representation per configured output
  path, with concrete `target_info` series naming for both the native Info and
  Gauge fallback encodings.
- Define identity and full profiles, including pull's identity-only status and
  the optional atomic Remote Write 2.0 and direct-ingestion capabilities.
- Define transport-specific validation, response counts, retry behavior,
  collision handling, sample schedules, and lifecycle output without changing
  their underlying protocol requirements.
- Define the consumer-first compatibility rollout and reserve future marker
  values for separate standardization.

## Round-Trip Guarantee and Limits

For supported ordinary points carried by an active v1 tuple, an identity-profile
path preserves the exact normalized scrape `job` and `instance`, provided the
final point does not collide and a receiving pull scrape preserves those
labels.

A full-profile path additionally preserves the individual presence and
non-empty string value of each covered service attribute obtained from valid
associated target metadata. The guarantee begins after ordinary protocol
validation and applies only to accepted supported points and coherent metadata
in one preserved Option C batch satisfying every full-profile requirement.

Neither profile reproduces the source target-info family or series, nor its
sample presence, timestamps, cadence, HELP, UNIT, start timestamps, or
exemplars. The consumer generates one canonical `target_info` representation
only as a Resource-metadata carrier. The semantic Info-versus-Gauge
representation is not itself part of the round-trip guarantee.

Neither profile covers protocol-invalid input, unsupported points, incomplete
scrape identity, malformed or unknown marked tuples, target-info-only input,
inactive or malformed target state, incompatible exact target-info types or
shapes, conflicting source metadata keys, empty or non-string covered values,
other target metadata, receiver- or exporter-added enrichment, external
labels, or processors that alter covered identity or metadata.

The guarantee also excludes points subject to actual final-series or
metric-family collisions, incompatible family definitions, lossy or colliding
label translation, disabled or noncanonical target-info output, missing usable
canonical timestamps, hard output limits, and explicit promotion of a control
attribute. A suffix-looking ordinary metric is excluded only if it encounters
such an actual output conflict, not merely because its name could be reduced to
`target_info`.

The full guarantee additionally excludes split, coalesced, or mixed Option C
batches; a source unit whose boundary was not established before transport
batching; Remote Write input or output without the applicable atomic-delivery
capability; Remote Write 1.0; pull output; cross-request target metadata; a
final composition layer unable to repeat complete validation; direct ingestion
that may partially commit the batch; and identity groups with conflicting
covered-attribute presence or values.

Historical series retirement, cross-operation metadata continuity, and
query-time uniqueness during label-set changes remain outside the guarantee
without relaxing existing staleness requirements. A verified stale marker that
retires an earlier canonical label set is lifecycle output rather than a second
active representation.

By default, the control tuple is neither copied to canonical target-info output
nor emitted as ordinary labels. Explicit promotion does not alter active tuple
identity handling, but the resulting ordinary label set is outside both
profiles' guarantees.
