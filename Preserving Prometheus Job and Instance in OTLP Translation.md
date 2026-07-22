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

Option C is a standalone contract using Option B's namespaced Resource names. It separates Prometheus
scrape identity from semantic service identity, never derives `service.*` from `job` or `instance`, and
leaves markerless native OTLP under `keep_identifying_resource_attributes`.

## Core Identity Contract

### Dependencies, Guarantees, and Vocabulary

Named dependencies are the **compatibility translation rules** in *Prometheus Metric points to OTLP* and
*OTLP Metric points to Prometheus* of the [compatibility specification](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/);
the [Prometheus exposition](https://prometheus.io/docs/instrumenting/exposition_formats/) and
[OpenMetrics](https://github.com/prometheus/OpenMetrics/blob/v1.0.0/specification/OpenMetrics.md)
**Prometheus/OpenMetrics rules**; the [Remote Write 1.0](https://prometheus.io/docs/specs/prw/remote_write_spec/) and
[2.0](https://prometheus.io/docs/specs/prw/remote_write_spec_2_0/) **Remote Write rules**; and the
[OTLP](https://opentelemetry.io/docs/specs/otlp/) and
[exporter](https://opentelemetry.io/docs/specs/otel/protocol/exporter/) **OTLP rules**. They apply unless
Option C explicitly overrides them.

The Resource control tuple is:

- `prometheus.scrape.identity.version = "1"`
- `prometheus.job = <normalized job>`
- `prometheus.instance = <normalized instance>`

An **active tuple** has marker string `"1"` and both pair members as non-empty strings; it never supplies
or defaults `service.name`, `service.namespace`, or `service.instance.id`.

An **attempt** is one observable scrape transaction, pull response, OTLP or Remote Write request, or direct
ingestion transaction. An **admitted batch** is every active Resource from accepted ordinary entities in one
original scrape or upstream transaction after applying accepted target metadata; it stays complete only
while they remain together. An **identity group** is the active Resources with one pair in an attempt; core
may group source transactions.

A **canonical series** is one physical target-info series with one final label set and one or more scheduled
samples. Its **canonical slot** is the final configured target-info name plus normalized pair within an
attempt. Timestamps and metadata labels do not distinguish slots, so different label sets compete. A
**contributing Resource** is an active Resource in the identity group with at least one supported ordinary
output point; it contributes timestamps whether or not it supplies surviving metadata.

| Profile | Guaranteed | Excluded |
| :---- | :---- | :---- |
| Identity | Exact normalized pair on accepted, supported, collision-free ordinary points and canonical series; pull requires the receiving scrape to preserve it | Protocol-invalid or unsupported points; incomplete identity; malformed tuples; final collisions; external labels; receiver enrichment; promoted control-label sets; semantic processors |
| Full | Identity plus exact presence and non-empty string value of each covered service attribute obtained from valid associated target metadata in one batch | Identity exclusions plus inactive, invalid, or conflicting source metadata; target-info exemplars; other target metadata; target-info-only input; lossy mapping; disabled/noncanonical output; missing schedule; control promotion; split/combined batches; pull; Remote Write 1.0; non-atomic transport; partial commit; cross-request metadata |

Both exclude source target-info presence, samples, timestamps, cadence, HELP, UNIT, start timestamps,
exemplars, representation, continuity, retirement, and query-time uniqueness during label changes. They
apply only to conforming-producer tuples; activation cannot prove provenance.

### Dispatch and Processing Order

Emission and recognition MUST be independent, disabled-by-default options scoped by endpoint or pipeline.
Consumer dispatch is exhaustive:

- Recognition disabled: apply the compatibility translation rules.
- Recognition enabled with no marker: apply those rules and do not reserve either pair member.
- Marker `"1"` with a complete pair: activate Option C and consume the tuple as control and identity.
- Empty, non-string, or unknown marker, or incomplete, empty, or non-string pair: reject the Resource under
  core or the attempt under atomic enforcement; never fall back.

Every valid tuple activates syntactically; endpoint scoping and collision inventory are safeguards. An
enabled producer MUST atomically replace same-named Resource values and never emit a partial tuple. Point
attributes remain ordinary and cannot activate Option C; metadata cannot supply controls. Consumed controls
are not metadata or ordinary labels by default.

Order is protocol negotiation/decoding/structural validation, relabeling/target filling/label validation,
source admission/accounting, core translation, optional atomic validation, then commit/response. Invalid
protocol input follows its Prometheus/OpenMetrics, Remote Write, or OTLP rules.

Admission normalizes identity under the compatibility translation or Remote Write rules, groups ordinary
points by exact pair, and excludes unsupported entities, incomplete pairs, and invalid, inactive,
conflicting, or unassociated metadata and exemplars. Exclusions never enter the batch. The producer stores
the tuple and accepted metadata on each Resource without duplicating identity as point attributes. Core
need not preserve the source boundary.

The tuple atomically supplies `job` and `instance` to associated ordinary points and canonical series,
overriding point-, exporter-, and service-derived identity.

For each semantic omission, rejection, or conflict, its deciding stage MUST emit exactly one **bounded
diagnostic** through an implementation-defined channel per affected source series, invalid Resource,
pair/key conflict, or slot in an attempt. Coalesce wire entities; series rejection owns exemplar diagnostics.
Independent retries may repeat diagnostics.

### Bidirectional Translation

| Direction | Scenario | Required core behavior |
| :---- | :---- | :---- |
| Prometheus to OTLP | Complete normalized pair, no target metadata | Store the active tuple; do not synthesize `service.*` |
| Prometheus to OTLP | Complete pair and valid associated target metadata | Store the tuple and metadata as Resource attributes; consume the target-info series |
| Prometheus to OTLP | Service-looking label only on an ordinary metric | Keep it as a data point attribute |
| Prometheus to OTLP | Incomplete normalized identity | Exclude that ordinary entity during admission; never emit a partial tuple |
| Prometheus to OTLP | Invalid, conflicting, or unassociable target metadata | Exclude the invalid series or conflicting key; valid siblings continue |
| Prometheus to OTLP | Emission disabled | Preserve compatibility translation, cache, and protocol responses |
| OTLP to Prometheus | Recognition disabled or marker absent, including service-only, bare-job, and point-level cases | Preserve compatibility translation without reserving the pair |
| OTLP to Prometheus | Valid active tuple | Use the pair atomically as authoritative `job` and `instance` |
| OTLP to Prometheus | Marker present but tuple invalid | Reject that Resource; never use compatibility fallback |
| OTLP to Prometheus | Active tuple has covered service attributes | Include them in an enabled canonical series |
| OTLP to Prometheus | Active tuple conflicts with point or exporter identity | The tuple wins atomically |
| OTLP to Prometheus | Control attribute explicitly promoted | Apply configured promotion; the resulting ordinary label set is outside both guarantees |

### Target Metadata Input

Use parser family/type evidence only while it describes the final relabeled scalar; otherwise use exact
final names. Classification never strips a type suffix.

| Final input evidence | Classification |
| :---- | :---- |
| Semantic `target` Info scalar whose concrete name is `target_info` | Accepted native metadata |
| Exact scalar `target_info` with Gauge, Info, unknown, or no type | Accepted fallback/flattened metadata |
| Remote Write 2.0 exact `target_info`, Gauge/Info/unset metadata, and scalar samples | Compatible metadata evidence |
| Exact `target_info` with another asserted type or histogram shape | Invalid reserved input |
| Semantic `target` Info with incompatible shape or assertion | Invalid reserved input |
| Flat exact `target`, with or without Info assertion | Ordinary noncanonical input |
| `target_info_total`, `target_info_bucket`, `target_info_sum`, `target_info_count`, or another suffix-looking name | Ordinary input |
| Any other name | Ordinary input |

For Remote Write 2.0, classify each `TimeSeries` fragment before grouping identical full labels, retaining
type, shape, samples, histograms, and exemplars. Gauge, Info, and unasserted scalar fragments are compatible;
another asserted type or shape invalidates the logical series. HELP, UNIT, and start timestamps are irrelevant.

Associate metadata only with ordinary series sharing the exact pair within one scrape transaction or one
complete Remote Write request. For each associated logical series:

- Select the greatest-timestamp scalar. A greatest-timestamp tie is one state only when all values are stale
  or all are non-stale `1`; otherwise it is invalid.
- A selected stale state is inactive; a non-stale state is valid only at value `1`.
- Remove name and identity labels; convert the rest by the compatibility translation rules, excluding controls.
- For each Resource key supplied by valid series, keep it only if all values agree; valid siblings continue.
- Consume recognized scalars as metadata; emit neither them nor empty target-info-only `ResourceMetrics`.

Target-info exemplars follow the exception below. Remote Write association is order-independent and
request-wide, with no cross-request metadata cache; request scope alone does not establish full eligibility.

### Canonical Prometheus Output

For each identity group, apply compatibility translation, exclude controls, and add the pair separately.
Present non-empty string `service.name`, `service.namespace`, and `service.instance.id` are candidates
regardless of `keep_identifying_resource_attributes`. Keep labels whose suppliers agree; absence is not a
conflict, but disagreement omits the label.

On a Prometheus output path, evaluate canonical generation in this order:

1. Apply configuration. Disabled, namespaced, or renamed generation follows that configuration under core,
   reserves no canonical slot, and exits this algorithm; the path is statically full-profile ineligible.
2. Determine necessity. Core requires a canonical series only when Resource metadata beyond the pair
   survives; full always requires one, including pair-only.
3. If core requires none, reserve no slot and retain ordinary `target_info` under compatibility translation.
4. Preflight schedule, limits, final mapping, and final composition validation. On failure, core preserves
   identity only, reserves no slot, and leaves ordinary `target_info` to compatibility handling; full rejects.
5. Resolve collisions. Under core, the canonical series owns its slot and every competing occupant is
   omitted, including different metadata label sets; other collisions use compatibility translation. Full
   rejects either collision.
6. Emit exactly one canonical series.

Each configured path MUST pin exactly one representation:

- Use semantic `target` Info when the path preserves Info; otherwise use a value-`1` `target_info` Gauge.
- Both have concrete name `target_info`; flat `target_info` with Info metadata represents family `target`.
- Never emit both, create Info family `target_info` (concrete `target_info_info`), or vary by attempt.
- Pull paths allowing non-Info formats MUST use Gauge or reject incompatible negotiation.

The schedule is:

- Pull: one value-`1` sample without an explicit timestamp.
- Remote Write: the ordered, deduplicated union of each contributing Resource's greatest supported point timestamp.
- Direct ingestion: half-lookback-delta intervals from the earliest through latest supported timestamps
  across contributing Resources.

Before mutation, validate all candidates after final namespace, rename, labels, identity, and type naming.
A composition-changing layer MUST recompute candidates and repeat the ordered algorithm.
Suffix-looking metrics remain ordinary unless they cause a physical-series or family collision.

Resource-to-label conversion uses the compatibility translation rules. Explicit promotion through
`promote_resource_attributes`, `promote_all_resource_attributes`, or equivalent settings does not change
tuple identity, but its ordinary label set is outside both guarantees. Exact covered-attribute
round-tripping requires an injective, UTF-8-preserving final label-name mapping.

Prometheus/OpenMetrics or Remote Write rules govern staleness and discontinuity. A verified stale marker
retiring a prior canonical label set is lifecycle output,
not an active competitor; arbitrary stale input has no exemption. Changing Info/Gauge is a metadata
compatibility event but creates no second concrete series when labels are unchanged. Label changes use
normal protocol lifecycle handling.

## Optional Full Profile

### Local Atomic Enforcement

**Atomic-batch enforcement** is a third, disabled-by-default option, scoped to an input, endpoint, or
output and requiring its Option C gate. Payloads cannot request or prove it.

Full conformance adds exact covered metadata and atomic handling of exactly one admitted batch whose boundary
predates batching, request assembly, queues, sharding, WALs, and retries. Wire protocols do not encode it:
local enforcement validates an attempt; deployment attestation establishes that its batch is original and
complete. If required attestation is unavailable, reject atomic-batch configuration as statically ineligible.

For each of `service.name`, `service.namespace`, and `service.instance.id`, every Resource in an identity
group MUST have identical presence and, when present, the same non-empty string value. Presence mismatch,
empty or non-string values, or disagreement fails the attempt; other metadata uses core merge-and-omit. All
three absent is valid and, on an eligible output path after successful preflight, produces a pair-only series.

Gate states are:

1. Disabled: apply core or compatibility translation.
2. Enabled without its emission/recognition gate, or on an incapable path: reject configuration.
3. Producer: admit source content, then validate and emit the batch atomically; exclusions are not members.
4. Markerless consumer/output attempt: apply compatibility translation.
5. Marked attempt: validate all co-resident entities, including markerless data; reject completely or commit atomically.

After decoding, unsupported or invalid content, collisions, limits, missing schedules, or unavailable final
mapping or composition validation fail the attempt, including markerless failures and malformed marked
Resources. Attested batch completeness and passive preservation are path eligibility requirements, not
per-entity runtime checks. Only producer admission may partially exclude content before a batch exists.

Retries MUST preserve batch membership and covered attributes; each attempt is independently atomic.
Request identity, deduplication, and exactly-once delivery follow Remote Write or OTLP rules. Split,
removed, combined, or partially visible batches violate the contract: reject detectable violations;
undetectable boundary loss makes the deployment nonconformant.

### Path Requirements and Attestation

| Path | Core behavior | Additional full-profile requirement |
| :---- | :---- | :---- |
| Scrape producer input | Emit active Resources from admitted ordinary entities | Preserve every admitted Resource from one scrape as one batch |
| OTLP forwarding or intermediary | Preserve each active tuple; ordinary batching and partial success remain available | Carry one attested batch per attempt without changing membership or covered attributes, splitting, combining, or partial success |
| Pull output | The receiving scrape preserves exposed `job` and `instance`, for example with `honor_labels: true` | Never eligible because accumulation and scrape timing lose the producer snapshot |
| Remote Write 1.0 input or output | Identity-profile eligible | Never full-profile eligible |
| Remote Write 2.0 producer input | Admit valid request entities independently | One pre-established source transaction per request; no cross-request metadata cache |
| Remote Write 2.0 output | Preserve tuple-derived labels | Sender, queue, WAL, retry path, and receiver preserve one batch per attempt and commit it atomically |
| Direct OTLP ingestion | Resource- or group-scoped partial success is permitted | Validate and commit one complete batch without partial success |

A passive intermediary needs no Option C gate but MUST be attested to preserve the table's full-profile
properties. A composition-changing processor is ineligible unless it recognizes Option C and performs
batch-aware enforcement; generic OTLP batching is insufficient. Active boundaries require atomic-batch
enforcement. Full conformance is therefore a closed-world deployment contract, not a wire property.

### Protocol Outcomes

**Scrape producer.** Post-protocol semantic admission failures emit their bounded diagnostic without changing
scrape success or `up`.

**Target-info exemplars on producer input.** Option C overrides compatibility conversion: reject exemplars
because the consumed series has no owning OTLP point, while accepting its scalar independently. The logical
series owns the diagnostic. If another rule rejects it, all its entities count as zero written and its
exemplars are neither recounted nor rediagnosed.

**Remote Write producer.** Count each sample, histogram, and exemplar once after grouping. An accepted
target-info scalar counts as written; an independently rejected exemplar counts as zero. Ordinary exemplars
follow compatibility translation. Validate before mutation or downstream consumption. Partial semantic
rejection returns permanent HTTP `400`; Remote Write 2.0 reports exact accepted counts or zero for total
rejection, while Remote Write 1.0 retains its specified response.

**Direct OTLP and atomic receivers.** Complete direct core rejection returns non-retryable gRPC
`InvalidArgument` or HTTP `400`; partial core ingestion reports exact `rejected_data_points`. Atomic sender
validation precedes enqueue/send. Atomic receiver rejection writes nothing, returns permanent HTTP `400`,
and reports zero Remote Write 2.0 counts. Direct OTLP atomic rejection returns non-retryable
`InvalidArgument`/HTTP `400` without partial success; transient transport or storage failures use Remote
Write or OTLP retry rules.

## Rollout and Specification Status

The controls require standardization before use. With both gates disabled, apply the compatibility
translation rules. Recognition alone leaves markerless Resources unchanged and activates supplied valid
tuples. Emission alone exposes controls to compatibility metadata, promotion, and collisions without an
identity override. With both enabled, valid tuples activate and malformed marked Resources fail at the
configured scope. Evaluate atomic-batch enforcement only after its corresponding gate.

Before recognition, inventory control-name and marker collisions, fan-in identity, explicit promotion, and
processors that alter tuples or covered metadata. Before atomic enforcement, verify or attest admission,
batch boundaries, label mapping, representation, final validation, limits, intermediaries, queues, retries,
and receiver atomicity. A complete-looking request proves none of these.

PromQL always selects concrete `target_info`; semantic family `target` is not a second query name.
Representation changes need compatibility review. Default dotted-to-underscore mappings are identity-only.

Adoption requires normative changes covering the controls and gates, admission and batches, dispatch,
target metadata and exemplars, protocol accounting, canonical-series rules, intermediaries, guarantees, and
rollout. Until then, the named specifications remain authoritative. New marker values, default-on gates,
or another canonical name require separate standardization.
