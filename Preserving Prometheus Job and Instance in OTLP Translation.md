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
section. The preceding Summary describes the bare-attribute proposal and is not
Option C's contract. The rules below are the complete Option C design.

Option C addresses Practical Issues 1 and 3 by storing Prometheus scrape
identity separately from semantic service identity and never deriving
`service.*` from `job` or `instance`. Practical Issue 2 for arbitrary native
OTLP data remains handled by `keep_identifying_resource_attributes`.

## Contract at a Glance

This table is a non-normative index to the rules that follow. Producers store
the normalized scrape identity as one Resource control tuple:

- `prometheus.scrape.identity.version = "1"`
- `prometheus.job = <normalized job>`
- `prometheus.instance = <normalized instance>`

| Direction | Scenario | Required result |
| :---- | :---- | :---- |
| Prometheus to OTLP | Complete normalized pair, no target metadata | Store the active tuple; do not synthesize `service.*` |
| Prometheus to OTLP | Complete pair and valid associated target metadata | Store the tuple and metadata as Resource attributes; consume the target-info series |
| Prometheus to OTLP | Service-looking label only on an ordinary metric | Keep it as a data point attribute |
| Prometheus to OTLP | Incomplete normalized identity | Omit that ordinary series; never emit a partial tuple |
| Prometheus to OTLP | Invalid, conflicting, or unassociable target metadata | Exclude the invalid series or conflicting key; valid siblings continue |
| Prometheus to OTLP | Producer emission disabled | Preserve complete legacy translation |
| OTLP to Prometheus | Recognition disabled or marker absent | Preserve complete legacy translation |
| OTLP to Prometheus | Valid active tuple from any source | Use the pair atomically as authoritative `job` and `instance` |
| OTLP to Prometheus | Marker present but tuple invalid | Fail closed under the configured enforcement scope |
| OTLP to Prometheus | Active tuple has covered service attributes | Include them in enabled canonical target-info output |
| OTLP to Prometheus | Active tuple conflicts with point or exporter identity | The tuple wins atomically |
| OTLP to Prometheus | Control attribute explicitly promoted | Apply existing promotion; the resulting ordinary label set is outside the guarantee |
| End to end | Pull preserves exposed identity | Identity-profile eligible only |
| End to end | One batch crosses an explicitly enforced atomic path | Full-profile eligible when every other requirement holds |
| End to end | Batch split, coalesced, mixed, partially committed, or exposed through pull state | Identity-profile only |

## Core Contract

### Control, Activation, and Provenance

A producer translates Prometheus or OpenMetrics input to OTLP and emits Option C
attributes. A consumer recognizes those attributes while translating OTLP to a
Prometheus representation or ingestion path.

The three Resource **control attributes** are the marker and reserved pair
shown above. An **active v1 tuple** has marker string `"1"` and both pair members
present as non-empty strings. The pair never supplies or defaults
`service.name`, `service.namespace`, or `service.instance.id`.

Producer emission and consumer recognition MUST be independent,
implementation-specific options that default to disabled and can be scoped to
an endpoint or pipeline. Consumer dispatch is exhaustive:

| Recognition and Resource state | Required behavior |
| :---- | :---- |
| Recognition disabled | Apply complete legacy identity, metadata, promotion, and collision behavior |
| Recognition enabled, marker absent | Apply complete legacy behavior; do not reserve either pair member |
| Marker exactly `"1"`, pair complete | Activate Option C and consume the tuple as control and identity |
| Marker present but empty, non-string, unknown, or paired with an incomplete, empty, or non-string pair | Fail closed as defined by the enforcement matrix; never use legacy fallback |

Activation is syntactic. A consumer cannot authenticate whether a tuple came
from a conforming producer, native OTLP input, a processor, or a user; every
valid tuple activates when recognition is enabled. Endpoint scoping and the
rollout collision inventory are the safeguards.

An enabled producer owns all three control values. It MUST atomically replace
same-named preexisting Resource attributes with its computed tuple and MUST NOT
emit a partial tuple. Point attributes with these names remain ordinary and
cannot activate Option C. Target metadata cannot supply the control keys.

For an active Resource, the control attributes are not emitted as target
metadata or ordinary metric labels by default. The round-trip profiles apply
only to tuples emitted by conforming producers, even though consumer activation
does not establish provenance.

### Batches and Identity Groups

An **Option C batch** contains all active Resources produced from one original
scrape transaction or one upstream transaction whose contents and boundary
exist before transport batching, queues, sharding, WAL persistence, retries, or
request assembly. An **identity group** contains the Resources in one batch
having the same reserved pair under full enforcement. Under identity
enforcement, it contains all active Resources with that pair in the complete
output operation, including Resources coalesced from several batches. The batch
boundary is not encoded in OTLP or Remote Write and cannot be reconstructed
after it is lost.

An output operation is one complete pull response, one logical Remote Write
request including retries, or one direct-ingestion transaction. An active
tuple supplies `job` and `instance` atomically on every associated ordinary
point and canonical target-info representation, overriding point-level,
exporter-added, and service-derived identity.

## Prometheus to OTLP

Ordinary protocol negotiation, decoding, structural validation, source
relabeling, scrape-target filling, and label validation happen before Option C.
Protocol-invalid input retains its existing failure behavior.

- Normalize `job` and `instance` using existing scrape or Remote Write rules.
  Omit an ordinary series unless both final values are non-empty.
- Group supported ordinary points by exact normalized pair. Store the active
  tuple on each Resource and do not also store source `job` or `instance` as
  data point attributes.
- Emit all active Resources from one source transaction together as one batch.
  Splitting or coalescing preserves identity behavior but loses full-profile
  eligibility.
- When Remote Write input cannot prove a pre-transport source boundary, emit
  its translated Resources together for identity processing without claiming
  that the request established a full-profile Option C batch.
- Keep a service-looking ordinary metric label as a data point attribute. Only
  valid associated target metadata supplies covered service Resource
  attributes.
- Never default a `service.*` attribute from `job` or `instance`.

When producer emission is disabled, preserve complete existing input, cache,
and response behavior.

## Target Metadata

Recognition uses parser-provided family and type evidence when available and
exact final series names otherwise. Evidence participates only when it still
describes the final scalar representation after relabeling. Classification
never strips a type-specific suffix.

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

For Remote Write 2.0, classify each `TimeSeries` fragment before combining
fragments with identical complete labels. Preserve each fragment's type, shape,
samples, histograms, and exemplars. Gauge, Info, and no-assertion scalar
fragments are compatible for exact `target_info`; any incompatible asserted
type or shape invalidates the complete same-label logical series. HELP, UNIT,
and optional start timestamps do not affect recognition after ordinary protocol
validation.

Associate target metadata only with ordinary series having the same exact pair
in the current producer association scope: one scrape transaction for scrape
input or one complete request for Remote Write input. For each associated
logical series:

- Select the greatest-timestamp scalar sample. Equal greatest timestamps form
  one state only when all are stale or all are non-stale with value `1`.
- Treat a selected stale state as inactive. A selected non-stale state is valid
  only when its value is `1`.
- Remove the metric name and identity labels. Convert remaining labels to
  Resource attributes using existing name and value rules, excluding the
  control keys.
- For each Resource key supplied by several valid series, retain one value when
  all suppliers agree; otherwise omit only that key. Valid siblings continue.
- Consume recognized scalar samples as metadata. Do not emit them as OTLP
  metrics, and do not emit empty `ResourceMetrics` for target-info-only input.

Source target-info presence, timestamps, cadence, HELP, UNIT, start timestamps,
and exemplars are not reproduced by Option C.

For Remote Write input, associate across the complete request independently of
series order and never use a cross-request target-info cache. Request-local
association alone does not prove a pre-transport source transaction or make the
input full-profile eligible.

## Canonical Prometheus Output

### Metadata Merge

For each identity group, compute target-info candidates with existing Resource
selection, conversion, final naming, and collision rules. Exclude the control
attributes and add the reserved pair separately as `job` and `instance`.

When target-info generation is enabled, every present covered service attribute
with a non-empty string value is a candidate regardless of
`keep_identifying_resource_attributes`. The covered attributes are
`service.name`, `service.namespace`, and `service.instance.id`.

In identity enforcement, include a final metadata label when all Resources that
supply it agree; absence elsewhere is not a conflict. Omit a conflicting label.
In full enforcement, every Resource in an identity group MUST have identical
presence and, when present, the same non-empty string value for each covered
attribute. Empty or non-string values fail full enforcement. Non-covered
metadata continues to use merge-and-omit in both modes.

### Representation and Schedule

A **canonical target-info output** is the single representation-independent
metadata position, or canonical slot, for one final pair in one output
operation. Each configured path MUST pin exactly one representation:

- Use semantic family `target` with Info type when the complete path preserves
  Info semantics; otherwise use the `target_info` Gauge fallback with value `1`.
- Both have concrete Prometheus series name `target_info`. In a flat format,
  concrete `target_info` plus Info metadata encodes semantic family `target`.
- Never emit both, never generate semantic Info family `target_info` (whose
  concrete sample would be `target_info_info`), and never vary the pinned
  representation by operation.
- A pull endpoint that permits formats without Info support MUST use the Gauge
  fallback for every response or reject incompatible negotiation.

Generate at most one active canonical label set per pair and output operation.
Full enforcement generates it even with no metadata beyond the pair; identity
enforcement need not. The output schedule is:

- Pull: one value-`1` sample without an explicit timestamp.
- Remote Write: each contributing Resource's greatest supported ordinary-point
  timestamp, unioned, deduplicated, and ordered for the identity group.
- Direct ingestion: the existing half-lookback-delta schedule from earliest to
  latest supported ordinary-point timestamps.

A timestamp-carrying identity operation with no usable canonical timestamp
omits canonical metadata. The same condition is a full-enforcement failure.

### Final Validation, Promotion, and Lifecycle

Before visible mutation, validate every ordinary, legacy, and canonical
candidate after final namespace, rename, label, identity, and type-specific
naming. Any later layer that changes composition MUST repeat merging, schedule,
slot, collision, and applicable full-enforcement validation.

Reserve canonical output against another exact final `target_info`
representation for the same pair. An exact ordinary, legacy, or markerless
competitor is omitted in identity enforcement; full enforcement fails the
operation. Suffix-looking metrics remain ordinary unless they cause an actual
final series or family collision. Identity enforcement applies existing
ordinary collision handling to such a collision; full enforcement fails the
operation.

If a final layer cannot repeat validation, identity enforcement omits generated
canonical output and remains identity-only; full enforcement fails. Disabled,
namespaced, or renamed target-info generation remains authoritative but is not
full-profile eligible.

Resource-to-label conversion remains orthogonal. Explicit promotion through
`promote_resource_attributes`, `promote_all_resource_attributes`, or equivalent
settings uses existing conversion and collision rules and does not change tuple
identity. The resulting ordinary label set is outside both guarantees. Exact
covered-attribute round-tripping requires an injective, UTF-8-preserving final
label-name mapping.

Existing staleness and series-discontinuation behavior remains unchanged. A
verified stale marker retiring a previous canonical label set is lifecycle
output, not a competing active representation; arbitrary stale ordinary or
legacy input receives no exemption. Changing the pinned Info/Gauge
representation is an explicit metric-metadata compatibility event but does not
create a second concrete series when the final label set is unchanged. Label-set
changes use the output protocol's normal lifecycle handling.

## Profiles, Transports, and Failures

### Enforcement and End-to-End Conformance

**Full-profile enforcement** MUST be a third implementation-specific option,
separate from emission and recognition. It defaults to disabled, is scoped to
the relevant producer input, consumer endpoint, or output path, and is valid
only with the corresponding Option C gate enabled. Payloads cannot request,
negotiate, or prove it. Without it, Option C uses identity enforcement.

Reject configuration when a local requirement is impossible. Pull output and
Remote Write 1.0 output cannot enable full enforcement. Remote Write 1.0 producer
input is also identity-only. Remote Write 2.0 requires local preservation of
one complete batch through limits, queues, WALs, sharding, and retries. Direct
ingestion requires validation and transactional commit of one complete
operation. Remote Write producer input additionally requires one pre-established
source transaction per 2.0 request and no cross-request target-info cache.

External receiver atomicity and multi-hop boundary preservation cannot be
verified locally; operators MUST attest them. **Full-profile conformance** is
the resulting end-to-end property when every Option C processing boundary
enables enforcement and every passive intermediary preserves, and is attested
to preserve, the required boundary. It is a closed-world deployment contract,
not a property supplied by either wire protocol.

**Identity-profile conformance** is the end-to-end property of a path whose
producers and consumers enable Option C and whose intermediaries preserve the
active tuple. It guarantees tuple-derived identity without claiming a preserved
source metadata snapshot.

| Path | Identity profile | Additional full-profile requirements |
| :---- | :---- | :---- |
| Scrape producer input | Emit active Resources from accepted ordinary series | One scrape transaction is emitted together as one Option C batch |
| Pull output | Receiving scrape preserves exposed `job` and `instance`, for example with `honor_labels: true` | Never eligible because accumulation and scrape timing lose the producer snapshot |
| Remote Write 1.0 input or output | Eligible for identity only | Never eligible |
| Remote Write 2.0 producer input | Translate valid entities in the request independently | One request contains exactly one pre-established source transaction and the input path preserves it |
| Remote Write 2.0 output | Preserve tuple-derived labels | One batch per request; sender, queue, WAL, retry path, and receiver preserve and atomically commit it |
| Direct OTLP ingestion | May retain Resource- or group-scoped partial success | One batch validated and committed as one transaction without partial success |

A full output operation contains exactly one complete Option C batch. Unrelated
non-Option-C data may share it only after complete final validation and shares
the operation's atomic success or failure. A statically incompatible
configuration fails startup. A dynamic semantic violation fails the complete
operation before visible mutation and never downgrades. Transient transport or
storage failures retain existing retryable response behavior.

### Failure and Diagnostic Matrix

| Condition | Identity enforcement | Full enforcement |
| :---- | :---- | :---- |
| Protocol decoding or structural invalidity before Option C | Existing protocol failure | Same |
| Incomplete source identity or invalid, conflicting, or unassociable source target metadata | Omit the affected series or key; valid siblings continue | Same; rejected source content is outside the full guarantee |
| Consumer Resource has present but invalid marker or pair | Reject that Resource; no legacy fallback | Reject the entire output operation |
| Covered output metadata is empty, non-string, or disagrees within an identity group | Omit the invalid or conflicting final label | Reject the entire output operation |
| Exact canonical competitor | Retain canonical output and omit the competitor | Reject the entire output operation |
| Other actual final series or family collision | Apply existing ordinary collision handling | Reject the entire output operation |
| Missing schedule, hard limit, lost batch boundary, or unavailable final validation | Omit canonical metadata where specified and retain identity-only output | Reject before visible mutation |
| Batch split or coalesced | Identity-only | Reject when detected; an undetectable loss makes the deployment nonconformant |

A **bounded diagnostic** is emitted at most once per affected logical source
series, invalid Resource, identity-pair-and-final-key conflict, or canonical
slot within the applicable source transaction or output operation. It is never
emitted once per point, sample, histogram, or exemplar.

Every semantic omission, rejection, or conflict in the matrix emits the
diagnostic at its applicable scope.

For scrape producer input, source semantic failures do not change scrape
success or `up`. For Remote Write producer input, count every wire sample,
histogram, and exemplar once even when fragments are grouped. A valid consumed
target-info scalar counts as written; an invalid attached exemplar is rejected
independently. Validate before shared-state mutation or downstream consumption.
Partial semantic rejection returns permanent HTTP `400` while Remote Write 2.0
reports exact nonzero counts for accepted siblings; total rejection reports zero.
Remote Write 1.0 retains its existing response format.

Sender-side full-output validation fails before enqueue or send. Receiver-side
full-operation semantic rejection writes nothing, returns permanent HTTP `400`,
and reports zero Remote Write 2.0 written counts. Direct OTLP full-operation
semantic rejection returns non-retryable gRPC `InvalidArgument` or HTTP `400`
without partial success. Completely rejected identity ingestion uses the same
non-retryable status; partial identity ingestion reports the exact
`rejected_data_points` count.

## Rollout, Guarantee, and Specification Status

The control names and behavior must be standardized before emission or
recognition. The independent gates support consumer-first rollout:

| Producer emission | Consumer recognition | Behavior |
| :---- | :---- | :---- |
| Disabled | Disabled | Complete legacy behavior |
| Disabled | Enabled | Markerless Resources remain legacy; any externally supplied valid tuple activates syntactically |
| Enabled | Disabled | Control attributes receive legacy metadata, promotion, and collision handling; no identity override |
| Enabled | Enabled | Every valid tuple activates; malformed marked Resources fail under the configured enforcement scope |

Before recognition, inventory all three control names, valid and malformed
marker collisions, fan-in identity collisions, explicit Resource promotion, and
processors that alter the tuple or covered metadata. Before full enforcement,
verify or attest the source boundary, label mapping, pinned representation,
final composition validation, request limits, queue and retry behavior, and
receiver atomicity. One apparently complete request proves none of these.

PromQL consumers select concrete `target_info` for either pinned representation;
they MUST NOT query both `target` and `target_info`. Representation changes need
compatibility review for family/type-aware consumers. Default mappings that
translate covered dotted names to underscores are identity-profile only.

| Profile | Guaranteed | Excluded |
| :---- | :---- | :---- |
| Identity | Exact normalized `job` and `instance` on accepted, supported, noncolliding points; pull also requires the receiving scrape to preserve them | Protocol-invalid or unsupported points, incomplete identity, malformed tuples, final collisions, external labels, enrichment, explicitly promoted control-label sets, and semantics-changing processors |
| Full | Identity guarantee plus individual presence and non-empty string value of each covered service attribute obtained from valid associated target metadata in one preserved batch | Every identity exclusion plus invalid or conflicting source metadata, other target metadata, target-info-only input, lossy label mapping, disabled/noncanonical output, missing schedule, promotion of control attributes, split/coalesced batches, pull, Remote Write 1.0, non-atomic transport, partial commit, and cross-request metadata |

Neither profile reproduces the source target-info family, series, samples,
timestamps, cadence, HELP, UNIT, start timestamps, exemplars, or Info-versus-
Gauge representation. Historical retirement, cross-operation continuity, and
query-time uniqueness during label-set changes are also outside the guarantee.

Adoption requires normative compatibility-specification changes for the control
keys, three default-disabled gates, dispatch rules, target classification,
canonical output, enforcement profiles, and rollout. Until those changes are
adopted, existing Prometheus/OpenMetrics compatibility specifications remain
authoritative. New marker values, default-on gates, or another canonical series
name require separate standardization.
