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

Option C is a standalone contract using Option B's namespaced Resource names. It separates
Prometheus scrape identity from semantic service identity and never derives `service.*` from `job` or
`instance`. Native OTLP still uses `keep_identifying_resource_attributes`.

## Core Identity Contract

### Guarantees and Vocabulary

The Resource control tuple:

- `prometheus.scrape.identity.version = "1"`
- `prometheus.job = <normalized job>`
- `prometheus.instance = <normalized instance>`

An **active tuple** has marker string `"1"` and both pair members as non-empty strings. The pair never
supplies or defaults `service.name`, `service.namespace`, or `service.instance.id`.

An **operation** is the path's local unit: one scrape transaction, pull response, OTLP request or ingestion
transaction, or logical Remote Write request including retries. An **admitted batch** contains every active
Resource produced from accepted ordinary entities in one original scrape or upstream transaction after
accepted associated target metadata is applied. It is complete only while all those Resources stay together.
An **identity group** contains the active Resources with one reserved pair in an operation; core may group
source transactions. Identity conformance needs enabled gates and tuple-preserving intermediaries; full adds atomicity.

| Profile | Guaranteed | Excluded |
| :---- | :---- | :---- |
| Identity | Exact normalized `job` and `instance` on accepted, supported, noncolliding ordinary points and any canonical target-info output; pull also requires the receiving scrape to preserve them | Protocol-invalid or unsupported points, incomplete identity, malformed tuples, final collisions, external labels, receiver-added enrichment, explicitly promoted control-label sets, and semantics-changing processors |
| Full | Identity plus, for each covered service attribute, its presence and, when present, exact non-empty string value as obtained from valid associated target metadata in one admitted batch | Every identity exclusion plus inactive (including stale), invalid, or conflicting source metadata; target-info exemplars; other target metadata; target-info-only input; lossy label mapping; disabled or noncanonical output; missing schedule; control-attribute promotion; split or combined batches; pull; Remote Write 1.0; non-atomic transport; partial commit of an admitted batch; and cross-request metadata |

Both guarantees exclude source target-info family/series presence, samples/timestamps/cadence,
HELP/UNIT/start timestamps/exemplars, representation, retirement/cross-operation continuity, and
query-time uniqueness during label changes. They apply only to conforming-producer tuples; activation
cannot prove provenance.

### Dispatch and Processing Order

Producer emission and consumer recognition MUST be independent, implementation-specific options that
default to disabled and can be scoped to an endpoint or pipeline. Consumer dispatch is exhaustive:

- Recognition disabled: apply all base Prometheus/OpenMetrics compatibility
  behavior.
- Recognition enabled with no marker: apply base behavior and do not reserve
  either pair member.
- Marker exactly `"1"` with a complete pair: activate Option C and consume the
  tuple as control and identity.
- Marker present but empty, non-string, unknown, or paired with an incomplete,
  empty, or non-string pair: reject that Resource under core behavior or the
  complete operation under atomic-batch enforcement; never use legacy fallback.

Activation is syntactic for every valid tuple; endpoint scoping and the rollout collision inventory are safeguards.

An enabled producer MUST atomically replace same-named Resource values with its tuple, never emitting it
partially. Same-named point attributes remain ordinary and cannot activate Option C; metadata cannot supply controls.
By default an active Resource does not emit consumed controls as metadata or ordinary labels.

Processing is ordered: protocol negotiation, decoding, structural validation, source relabeling,
scrape-target filling, and label validation; producer source admission and response accounting; core
translation; optional atomic-batch validation; then commit and response. Protocol-invalid input follows
the named base protocol rule.

Producer admission normalizes identity under base scrape or Remote Write rules, groups ordinary points by
exact pair, and excludes unsupported entities, incomplete pairs, and the invalid, inactive, conflicting,
or unassociated metadata and exemplars below. Exclusions do not enter the batch.
The producer then stores the tuple and accepted metadata on each Resource without duplicating source
identity as point attributes. Core behavior need not preserve the source boundary.

The tuple atomically supplies `job` and `instance` on every associated ordinary point and canonical
target-info output, overriding point-level, exporter-added, and service-derived identity.

A **bounded diagnostic** is emitted once at most per affected logical source series, invalid Resource,
identity-pair-and-final-key conflict, or canonical slot in its transaction or operation, never per wire entity.

### Bidirectional Translation

| Direction | Scenario | Required core behavior |
| :---- | :---- | :---- |
| Prometheus to OTLP | Complete normalized pair, no target metadata | Store the active tuple; do not synthesize `service.*` |
| Prometheus to OTLP | Complete pair and valid associated target metadata | Store the tuple and metadata as Resource attributes; consume the target-info series |
| Prometheus to OTLP | Service-looking label only on an ordinary metric | Keep it as a data point attribute |
| Prometheus to OTLP | Incomplete normalized identity | Exclude that ordinary entity during admission; never emit a partial tuple |
| Prometheus to OTLP | Invalid, conflicting, or unassociable target metadata | Exclude the invalid series or conflicting key; valid siblings continue |
| Prometheus to OTLP | Producer emission disabled | Preserve complete base translation, cache, and response behavior |
| OTLP to Prometheus | Recognition disabled or marker absent | Preserve complete base translation |
| OTLP to Prometheus | Valid active tuple | Use the pair atomically as authoritative `job` and `instance` |
| OTLP to Prometheus | Marker present but tuple invalid | Reject that Resource; never use legacy fallback |
| OTLP to Prometheus | Active tuple has covered service attributes | Include them in enabled canonical target-info output |
| OTLP to Prometheus | Active tuple conflicts with point or exporter identity | The tuple wins atomically |
| OTLP to Prometheus | Control attribute explicitly promoted | Apply configured promotion; the resulting ordinary label set is outside both guarantees |

### Target Metadata Input

Use parser-provided family and type evidence when it still describes the final scalar after relabeling;
otherwise use exact final names. Never strip a type-specific suffix during classification.

| Final input evidence | Classification |
| :---- | :---- |
| Family-aware semantic `target` Info with scalar point whose concrete text/flat sample is `target_info` | Accepted native target metadata |
| Exact scalar `target_info` with Gauge, Info, unknown, absent, or unspecified type | Accepted fallback or flattened native metadata |
| Remote Write 2.0 exact `__name__="target_info"`, Gauge, Info, or unspecified metadata, and scalar samples | Compatible target-metadata evidence |
| Exact `target_info` with another asserted type or histogram shape | Invalid reserved input |
| Family-aware `target` Info with incompatible shape or assertion | Invalid reserved input |
| Flat exact `target`, with or without Info assertion | Ordinary noncanonical input |
| `target_info_total`, `target_info_bucket`, `target_info_sum`, `target_info_count`, or another suffix-looking name | Ordinary input |
| Any other name | Ordinary input |

For Remote Write 2.0, classify each `TimeSeries` fragment before grouping identical complete labels,
retaining its type, shape, samples, histograms, and exemplars. Gauge, Info, and no-assertion scalar
fragments are compatible for exact `target_info`; any other asserted type or shape invalidates the
logical series. HELP, UNIT, and optional start timestamps do not affect recognition.

Associate target metadata only with ordinary series having the same exact pair
in the current producer association scope: one scrape transaction for scrape
input or one complete request for Remote Write input. For each associated
logical series:

- Select the greatest-timestamp scalar sample. Equal greatest timestamps form one state only when all are
  stale or all are non-stale with value `1`; other ties are invalid.
- Treat a selected stale state as inactive. A selected non-stale state is valid
  only when its value is `1`.
- Remove the metric name and identity labels. Convert remaining labels under
  *Prometheus Metric points to OTLP / Resource Attributes*, excluding controls.
- For each Resource key supplied by several valid series, retain one value when
  all suppliers agree; otherwise omit only that key. Valid siblings continue.
- Consume recognized scalar samples as metadata. Do not emit them as OTLP
  metrics, and do not emit empty `ResourceMetrics` for target-info-only input.

Recognized target-info exemplars follow the exception and accounting rule below.

For Remote Write input, associate request-wide regardless of series order and never cache target metadata
across requests. Request scope alone neither proves a source transaction nor establishes full eligibility.

### Canonical Prometheus Output

For each identity group, compute candidates under *OTLP Metric points to Prometheus / Resource
Attributes* and *Metric Attributes*. Exclude controls and add the reserved pair separately. Present
non-empty string `service.name`, `service.namespace`, and `service.instance.id` are candidates regardless
of `keep_identifying_resource_attributes`. Include a final label when its suppliers agree; absence
elsewhere is not a conflict. Omit disagreements.

When target-info generation is enabled and final mapping, scheduling, limits, and validation succeed,
core MUST generate exactly one canonical target-info output if a final Resource-derived label beyond the
pair survives merging, and MUST NOT generate pair-only output. On an output path, atomic-batch
enforcement MUST generate exactly one output even when the pair is its only metadata.

Each configured path MUST pin exactly one representation:

- Use semantic family `target` with Info type when the complete path preserves
  Info semantics; otherwise use the `target_info` Gauge fallback with value `1`.
- Both representations have concrete series name `target_info`. In a flat
  format, concrete `target_info` with Info metadata encodes semantic family
  `target`.
- Never emit both, generate semantic Info family `target_info` (which would
  produce concrete `target_info_info`), or vary the representation by operation.
- A pull endpoint that permits formats without Info support MUST always use the
  Gauge fallback or reject incompatible negotiation.

The schedule is:

- Pull: one value-`1` sample without an explicit timestamp.
- Remote Write: the union, deduplicated and ordered, of each contributing
  Resource's greatest supported ordinary-point timestamp.
- Direct ingestion: the receiver's half-lookback-delta schedule from the
  earliest to latest supported ordinary-point timestamps.

A timestamp-carrying operation with no usable canonical timestamp, or a hard
limit that prevents canonical output, omits it under core behavior and rejects
the operation under atomic-batch enforcement.

Before visible mutation, validate every ordinary, legacy, and canonical
candidate after final namespace, rename, label, identity, and type-specific
naming. Any later layer that changes composition MUST repeat merge, schedule,
slot, collision, and applicable atomic-batch validation.

Only required canonical output reserves its final `target_info` slot. Under
core behavior, retain required canonical output and omit an exact ordinary,
legacy, or markerless competitor for the same pair. When core requires no
canonical output, an ordinary `target_info` remains ordinary. Suffix-looking
metrics remain ordinary unless they cause an actual final series or family
collision; core applies the compatibility specification's collision handling.
Atomic-batch enforcement rejects either collision.

If a final composition-changing layer cannot repeat validation, core omits
generated canonical output and retains identity only; atomic-batch enforcement
rejects the operation. Disabled, namespaced, or renamed target-info generation
remains authoritative but is not full-profile eligible.

Resource-to-label conversion remains orthogonal. Explicit promotion through
`promote_resource_attributes`, `promote_all_resource_attributes`, or equivalent
settings uses the compatibility specification's conversion and collision rules
and does not change tuple identity. The resulting ordinary label set is outside
both guarantees. Exact covered-attribute round-tripping requires an injective,
UTF-8-preserving final label-name mapping.

The output protocol's staleness and series-discontinuation rules remain. A
verified stale marker retiring a previous canonical label set is lifecycle
output, not a competing active representation; arbitrary stale ordinary or
legacy input receives no exemption. Changing the pinned Info/Gauge
representation is a metric-metadata compatibility event but does not create a
second concrete series when the final label set is unchanged. Label-set changes
use the output protocol's normal lifecycle handling.

## Optional Full Profile

### Atomic-Batch Enforcement

**Atomic-batch enforcement** MUST be a third implementation-specific option,
separate from producer emission and consumer recognition. It defaults to
disabled, is scoped to the relevant producer input, consumer endpoint, or
output path, and requires the corresponding Option C gate. Payloads cannot
request, negotiate, or prove it.

Full-profile conformance preserves one admitted batch whose original boundary predates transport batching,
request assembly, queues, sharding, WAL persistence, or retries. OTLP and Remote Write do not encode or prove it.

Within each identity group, every Resource MUST have identical presence and, when present, the same
non-empty string value for each of `service.name`, `service.namespace`, and `service.instance.id`. Empty,
non-string, or disagreeing values fail the operation. Non-covered metadata uses core merge-and-omit behavior.

The gate follows this state machine:

1. When disabled, use core or base dispatch.
2. When enabled without the corresponding emission or recognition gate, or on
   a statically incapable path, reject the configuration before accepting
   operations.
3. On a capable producer path, perform source admission, then validate and emit
   its one admitted batch atomically; exclusions are not batch members.
4. On a capable consumer or output path, an operation with no marked Resource
   remains entirely base behavior. Otherwise require exactly one complete
   admitted batch and validate every co-resident entity, including markerless
   data, before mutation.
5. Reject an invalid operation completely and never downgrade to core. Commit a
   valid operation atomically.

After successful protocol decoding, any consumer or output entity that is
unsupported, invalid, colliding, over a hard limit, missing a required schedule,
or impossible to validate makes the operation invalid. This includes
base validation failures in unrelated markerless data. Malformed marked
Resources also participate and fail the operation. Producer source-admission
exclusions are the only permitted partial exceptions before a batch exists.

Splitting or removing admitted Resources, combining batches, or permitting
partial visibility violates the contract. Detectable violations reject the
operation; undetected violations make the deployment nonconformant.

### Path Requirements

| Path | Core behavior | Additional atomic-batch requirement |
| :---- | :---- | :---- |
| Scrape producer input | Emit active Resources from admitted ordinary entities | Preserve all admitted Resources from one scrape as one batch |
| OTLP forwarding or intermediary | Preserve each active tuple; ordinary batching and partial success remain available | Preserve exactly one batch per request or transaction, without changing membership or covered attributes, splitting, combining, or partial success; retries preserve the same batch |
| Pull output | Receiving scrape preserves exposed `job` and `instance`, for example with `honor_labels: true` | Never eligible because accumulation and scrape timing lose the producer snapshot |
| Remote Write 1.0 input or output | Identity-profile eligible | Never full-profile eligible |
| Remote Write 2.0 producer input | Admit valid request entities independently | Exactly one pre-established source transaction per request; preserve its admitted batch without cross-request metadata caching |
| Remote Write 2.0 output | Preserve tuple-derived labels | One batch per request; sender, queue, WAL, retry path, and receiver preserve and atomically commit it |
| Direct OTLP ingestion | Resource- or group-scoped partial success is permitted | Validate and commit one complete batch as one transaction without partial success |

A passive intermediary needs no Option C gate but MUST be attested to preserve
the table's full-profile properties. A composition-changing processor is
ineligible unless it recognizes Option C and provides batch-aware enforcement;
generic OTLP batching is insufficient. Active boundaries require atomic-batch
enforcement. External atomicity and passive preservation require attestation,
making the full profile a closed-world deployment contract, not a wire property.

### Protocol Outcomes

Producer source-admission failures emit the bounded diagnostic but do not change
scrape success or `up`. After protocol validation, each exemplar on an otherwise
valid target-info logical series is independently rejected because the consumed
series produces no OTLP data point that can own it. This is an Option C exception
to ordinary Prometheus exemplar conversion: scalar acceptance is independent,
and the exemplars share one diagnostic for that logical series. If another rule
rejects the logical series, its wire entities count as zero written; do not
count or diagnose its exemplars again.

Remote Write producer input counts every sample, histogram, and exemplar exactly
once even when fragments are grouped. A valid consumed target-info scalar counts
as written; an independently rejected target-info exemplar counts as zero.
Ordinary exemplars follow the compatibility specification. Validate before
shared-state mutation or downstream consumption. Partial semantic rejection
returns permanent HTTP `400`; Remote Write 2.0 reports exact nonzero written
counts for accepted siblings, while total rejection reports zero. Remote Write
1.0 retains its specified response format.

Completely rejected direct core ingestion returns non-retryable gRPC
`InvalidArgument` or HTTP `400`; partial core ingestion reports the exact
`rejected_data_points` count.

Atomic sender validation fails before enqueue or send. Atomic receiver rejection
writes nothing, returns permanent HTTP `400`, and reports zero Remote Write 2.0
written counts. Direct OTLP atomic rejection returns non-retryable gRPC
`InvalidArgument` or HTTP `400` without partial success. Transient transport or
storage failures retain the base protocol's retryable response behavior.

## Rollout, Guarantees, and Specification Status

The control names and behavior must be standardized before emission or
recognition. The independent gates support consumer-first rollout. With both
disabled, retain complete base behavior. Recognition alone leaves markerless
Resources unchanged but syntactically activates any externally supplied valid
tuple. Emission alone exposes the control attributes to base metadata,
promotion, and collision handling without an identity override. With both
enabled, every valid tuple activates and malformed marked Resources fail at the
configured scope. Atomic-batch enforcement is evaluated only after its
corresponding gate.

Before recognition, inventory all three control names, valid and malformed
marker collisions, fan-in identity collisions, explicit Resource promotion,
and processors that alter tuple, batch membership, or covered metadata. Before
atomic-batch enforcement, verify or attest source admission and boundaries,
label mapping, pinned representation, final composition validation, request
limits, OTLP intermediaries, queue and retry behavior, and receiver atomicity.
One apparently complete request proves none of these.

PromQL consumers select concrete `target_info` for either pinned representation;
they MUST NOT query both `target` and `target_info`. Representation changes need
compatibility review for family/type-aware consumers. Default mappings that
translate covered dotted names to underscores are identity-profile only.

Adoption requires normative compatibility-specification changes for the three
control keys; the emission, recognition, and atomic-batch gates; source
admission and admitted-batch definition; dispatch; target classification and
association; the target-info exception to ordinary Prometheus exemplar
conversion and its independent scalar/exemplar acceptance; Remote Write
written-count and response-status accounting; deterministic canonical output;
OTLP intermediary requirements; atomic-batch enforcement; both guarantees; and
rollout. Until adopted, the current Prometheus/OpenMetrics compatibility and
Remote Write specifications remain authoritative. New marker values, default-on
gates, or another canonical series name require separate standardization.
