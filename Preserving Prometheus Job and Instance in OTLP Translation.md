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

Option C is authoritative when selected; earlier A/B rules and summary add nothing to active tuples. It uses
Option B's names, separates scrape and service identity, never derives `service.*` from the pair, and delegates
disabled or markerless cases to compatibility.

## Core Identity Contract

### Dependencies and Guarantees

Unless overridden here, use the [compatibility translation rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/),
[Prometheus exposition](https://prometheus.io/docs/instrumenting/exposition_formats/) and
[OpenMetrics](https://github.com/prometheus/OpenMetrics/blob/v1.0.0/specification/OpenMetrics.md) rules,
[Remote Write 1.0](https://prometheus.io/docs/specs/prw/remote_write_spec/) and
[2.0](https://prometheus.io/docs/specs/prw/remote_write_spec_2_0/) rules, and [OTLP](https://opentelemetry.io/docs/specs/otlp/)
[exporter rules](https://opentelemetry.io/docs/specs/otel/protocol/exporter/).

The Resource control tuple is:

- `prometheus.scrape.identity.version = "1"`
- `prometheus.job = <normalized job>`
- `prometheus.instance = <normalized instance>`

| Profile | Guaranteed | Excluded |
| :---- | :---- | :---- |
| Identity | Exact pair on accepted, supported, collision-free ordinary points and canonical series; pull requires receiver preservation | Invalid/unsupported points; incomplete or malformed identity; collisions; external labels; enrichment; promoted controls; semantic processors |
| Full | Identity plus exact presence and non-empty string value of each covered service attribute from valid associated target metadata in one envelope | Identity exclusions plus invalid, inactive, or conflicting metadata; target-info exemplars or target-info-only input; other metadata; lossy mapping; noncanonical output; missing schedule; promotion; changed envelopes; pull; Remote Write 1.0; non-atomic transport; partial commit; cross-request metadata |

Neither preserves source target-info presence, samples, timing, HELP, UNIT, start timestamps, exemplars,
representation, continuity, retirement, or query-time uniqueness during label changes. Guarantees apply only
to conforming-producer tuples; activation cannot prove provenance.

### Terms and Translation Flow

| Term | Meaning |
| :---- | :---- |
| Normalized pair | Exact non-empty final `job` and `instance` label values after applicable relabeling, target filling, and label validation; Option C performs no further value rewriting and never derives either from `service.*` |
| Active tuple | Marker `"1"` and a normalized pair |
| Attempt | One scrape, pull response, OTLP/Remote Write request, or direct transaction |
| Identity group | Active Resources with one pair in an attempt; core may group source transactions |
| Ordinary candidate | Locally valid, final-mapped point before cross-series collisions |
| Ordinary survivor | Candidate retained by ordinary-only compatibility collision handling |
| Contributing Resource | Active Resource with a survivor; only survivor timestamps contribute |
| Canonical metadata supplier | Contributing Resource containing the relevant Resource attribute |
| Canonical metadata labels | Final-mapped Resource labels after control removal and merging across canonical metadata suppliers, excluding the pair |
| Canonical series | One physical target-info series with one label set and one or more scheduled samples |
| Canonical slot | Final target-info name plus pair in an attempt; metadata and timestamps do not distinguish slots |

Emission and recognition are independent, disabled-by-default endpoint or pipeline options. Recognition disabled
or markerless uses compatibility translation without reserving the pair. Marker `"1"` plus a complete pair
activates; other present markers or malformed pairs reject the Resource under core or the envelope under Full,
without fallback. Point attributes and metadata cannot activate or supply controls.

A producer uses three ordered phases:

1. **Source finalization.** Negotiate and decode the protocol, perform structural validation, apply applicable
   relabeling, target filling, and label validation, and extract the normalized pair. Classify reserved target
   metadata before grouping. For Remote Write 2.0, classify every `TimeSeries` fragment independently, then group
   only compatible target-metadata fragments with identical complete final labels. Independently assemble only
   ordinary metric fragments into logical histograms, summaries, and other families under compatibility and
   protocol rules.
2. **Semantic admission.** Group the resulting ordinary logical entities by normalized pair, associate and
   resolve target metadata within one scrape or complete Remote Write request, make each semantic admission or
   exclusion decision once per logical series, and compute provisional sample, histogram, and exemplar tallies.
3. **Materialization and final accounting.** Construct Resources, atomically replace same-named controls, attach
   accepted metadata, and, when enabled, form and validate the Full envelope. Only after all applicable Option C
   validation may the producer finalize each source entity's accepted or rejected decision and derive protocol
   counts and responses from those final decisions.

Unsupported or incomplete entities and invalid, inactive, conflicting, or unassociated metadata and exemplars
are excluded. Consumed metadata, exclusions, and identity point attributes are not emitted. Core may lose source
boundaries.

The tuple supplies authoritative `job` and `instance` atomically to candidates and canonical series. It never
supplies `service.*`; controls are not ordinary metadata or labels by default.

Each semantic omission, rejection, or conflict MUST produce one implementation-defined **bounded diagnostic**
per affected source series, invalid Resource, pair/key conflict, or slot. Coalesce wire entities; series
rejection owns exemplar diagnostics. Retries may repeat them.

Prometheus → OTLP:

| Scenario | Required core behavior |
| :---- | :---- |
| Complete pair; no target metadata | Store tuple; synthesize no `service.*` |
| Complete pair; valid target metadata | Store tuple and metadata; consume target-info |
| Service-looking ordinary label | Keep as point attribute |
| Incomplete identity | Exclude entity; emit no partial tuple |
| Invalid, conflicting, or unassociated metadata | Exclude invalid series or key; keep valid siblings |
| Target-info only | Exclude unassociated series; emit no empty `ResourceMetrics` |
| Emission disabled | Preserve compatibility behavior and responses |

OTLP → Prometheus:

| Scenario | Required core behavior |
| :---- | :---- |
| Recognition disabled or markerless, including service-only, bare-job, and point-label cases | Use compatibility translation; reserve no pair |
| Active tuple | Use pair as authoritative `job` and `instance` |
| Invalid marked tuple | Reject Resource; never fall back |
| Covered service attributes | Include in enabled canonical series |
| Active Resources with and without ordinary survivors | Merge canonical metadata and schedule only from contributing Resources |
| Point or exporter identity conflict | Tuple wins atomically |
| Disabled, namespaced, or renamed canonical generation | Follow core configuration; ineligible for Full |
| Occupied canonical slot | Canonical owns slot under core; Full rejects |
| Explicit control promotion | Apply promotion; ordinary label set is outside both guarantees |

### Target Metadata Input

Use parser family/type evidence only for the final relabeled scalar; otherwise use its exact name. Never
strip type suffixes.

| Final input evidence | Classification |
| :---- | :---- |
| Semantic `target` Info with concrete `target_info` | Native metadata |
| Exact scalar `target_info` with Gauge, Info, unknown, or no type | Fallback metadata |
| Remote Write 2.0 exact scalar `target_info` with Gauge/Info/unset metadata | Compatible metadata |
| Exact `target_info` with another type or histogram shape | Invalid reserved input |
| Semantic `target` Info with incompatible shape or assertion | Invalid reserved input |
| Flat exact `target`, even with Info assertion | Ordinary noncanonical input |
| A suffix-looking name such as `target_info_total` or `target_info_bucket` | Ordinary input |
| Any other name | Ordinary input |

For Remote Write 2.0, classify fragments before grouping identical full labels, retaining type, shape, and entities.
Gauge, Info, and unasserted scalar fragments are compatible; another type or shape invalidates the series.
HELP, UNIT, and start timestamps are irrelevant.

Associate only with ordinary series sharing the exact pair in one scrape or complete Remote Write request:

- Select the greatest-timestamp scalar. A tie is valid only when all are stale or all are non-stale `1`.
- Stale is inactive; non-stale is valid only at `1`.
- Remove name, identity, and controls; convert remaining labels by compatibility rules.
- Keep each supplied Resource key only when all suppliers agree; valid siblings continue.
- Consume recognized scalars.

Target-info exemplars follow the exception below. Remote Write association is order-independent and
request-wide without cross-request caching; request scope alone does not establish full eligibility.

### Prometheus Output

For each identity group, build canonical metadata labels through compatibility Resource conversion across
canonical metadata suppliers. Non-empty string `service.name`, `service.namespace`, and `service.instance.id`
remain candidates regardless of `keep_identifying_resource_attributes`. Exclude controls and add the pair
separately.

Evaluate the following table from top to bottom. A terminal result stops the Option C canonical branch.

| Stage | Core | Full |
| :---- | :---- | :---- |
| 1. Ordinary output | Resolve final output configuration, build candidates with tuple identity, apply ordinary-only compatibility collisions, and retain the survivors. | Do the same, but reject the envelope on an ordinary collision. |
| 2. Canonical configuration | If generation is disabled, namespaced, or renamed, follow configured core behavior, including any namespaced or renamed target-info generation; retain ordinary survivors, reserve no canonical slot, and stop. | The path is ineligible for Full. |
| 3. Full preflight | Apply no additional check. | Apply the raw-attribute and mapping invariants below; static inability makes the path ineligible, and an envelope-specific violation rejects the envelope. |
| 4. Metadata merge and mapping | Merge only canonical metadata suppliers; keep a final label when its present suppliers agree, with absence not a conflict. Mapping failure retains ordinary survivors, including compatibility-translated `target_info`, reserves no slot, and stops. | Merge the same suppliers after preflight. Failure to preserve or validate a covered mapping rejects the envelope; non-covered metadata retains core merge-and-omit behavior. |
| 5. Canonical necessity | If no canonical metadata label remains, retain ordinary `target_info` compatibility behavior, reserve no slot, and stop. | Continue with pair-only canonical output. |
| 6. Schedule and composition | No contributing Resource or usable timestamp, an unusable schedule, a hard limit, or unavailable validation retains ordinary survivors, reserves no slot, and stops. | Any such failure rejects the envelope. |
| 7. Slot and family collisions | Canonical output owns its slot and omits every occupant; other collisions use compatibility behavior. | Any collision rejects the envelope. |
| 8. Emission | Emit exactly one canonical series only after all prior stages succeed. | Emit exactly one canonical series only after all prior stages succeed. |

Canonical paths MUST pin exactly one representation:

- Use semantic `target` Info when preserved; otherwise use a value-`1` `target_info` Gauge.
- Both are concretely `target_info`; flat Info metadata denotes family `target`.
- Never emit both, emit concrete `target_info_info`, or vary by attempt.
- A pull path allowing non-Info formats MUST use Gauge or reject negotiation.

The schedule is:

- Pull: one value-`1` sample without an explicit timestamp.
- Remote Write: the ordered, deduplicated union of each contributing Resource's greatest survivor timestamp.
- Direct ingestion: use the schedule below across all contributing Resources.

For direct ingestion, let `D` be the path's configured PromQL lookback delta, or that path's documented default
when unset, and let `I = D / 2` under the path's duration arithmetic. The path keeps `D` fixed for the attempt.
`I` MUST be positive and advance the output timestamp at its supported precision; otherwise schedule preflight
fails. Let `t_min` and `t_max` be the earliest and latest survivor timestamps. Emit at `t_min + kI` for integers
`k >= 0` while the timestamp is strictly less than `t_max`, then emit `t_max` exactly once. Deduplicate equal
label-set and timestamp output after conversion to output precision. The strict bound, final append, and
deduplication prevent endpoint duplication for every interval length.

Validate before mutation. Composition-changing layers MUST rebuild candidates and survivors and repeat the
algorithm. Suffix-looking metrics remain ordinary unless they collide.

Resource-to-label conversion uses compatibility rules. Explicit `promote_resource_attributes`,
`promote_all_resource_attributes`, or equivalent promotion does not change tuple identity, but puts the
ordinary label set outside both guarantees. Full's additional mapping requirements are defined below.

Protocol rules govern staleness and discontinuity. A verified stale marker retiring prior canonical labels
is lifecycle output, not an active competitor; arbitrary stale input is not exempt. Info/Gauge changes are
metadata events and create no second concrete series with unchanged labels. Label changes use normal lifecycle
handling.

## Optional Full Profile

### Atomic Envelope and Eligibility

**Atomic-batch enforcement** is a third, disabled-by-default option, scoped to an input, endpoint, or
output and requiring its Option C gate. Payloads cannot request or prove it.

An **atomic envelope** is the producer's post-admission OTLP output, or every decoded marked and markerless
entity presented together to a marked consumer before semantic validation. Consumed source metadata and
pre-admission exclusions are not members. Full handles one original envelope whose boundary predates
requests, batching, queues, sharding, WALs, and retries.

Full applies two invariants before Resource conversion or merge-and-omit:

- **Raw covered attributes.** For each covered attribute (`service.name`, `service.namespace`, and
  `service.instance.id`), every active Resource in the atomic envelope's identity group MUST have identical
  presence and, when present, one non-empty string value. Mismatch, empty or non-string values, or disagreement
  rejects the envelope. A producer entity excluded before envelope formation is not a member and does not
  participate.
- **Covered mapping.** The attested forward and reverse path MUST recover every present covered Resource key
  exactly and preserve its string value byte-for-byte. Final label-name conversion MUST be provably reversible
  and injective for covered outputs relative to every Resource key the path can emit. A covered final label MUST
  NOT collide with another emitted canonical label, `job`, `instance`, `__name__`, or another protocol-reserved
  label. A statically lossy or non-injective configuration is ineligible for Full; an envelope-dependent violation
  rejects that envelope before merge or mutation. Default dotted-to-underscore conversion is therefore
  Identity-only.

All covered attributes may be absent. After both invariants pass, non-covered metadata uses core contributor
merge-and-omit behavior. Full emits after preflight and is pair-only exactly when no canonical metadata label
remains.

Atomic enforcement requires its emission or recognition gate and a locally capable component. Otherwise
configuration is invalid. Disabled enforcement uses core or compatibility behavior. An enabled producer
admits before forming and atomically emitting the envelope; a markerless consumer uses compatibility; a
marked envelope validates and rejects or commits every member. Only pre-envelope producer admission is partial.

Local enforcement validates local properties. Full conformance also requires explicit operator attestation
of every non-local intermediary, queue, WAL, retry path, and receiver; without it the deployment MUST NOT
claim full. Payloads and components do not infer attestation. After formation, invalid content, collisions,
limits, missing schedules, or unavailable mapping or composition validation reject the envelope. Detectable
boundary violations reject; undetectable ones make the deployment nonconformant.

Retries MUST preserve membership and covered attributes; each attempt is atomic. Protocol rules govern
request identity, deduplication, and exactly-once delivery. Split, removed, combined, or partial envelopes
violate the contract.

### Path Requirements and Attestation

| Path | Core behavior | Additional full-profile requirement |
| :---- | :---- | :---- |
| Scrape producer | Emit active Resources from admitted ordinary entities | Form one post-admission scrape envelope |
| OTLP intermediary | Preserve active tuples; batching and partial success remain available | Preserve one attested envelope, all members, and covered attributes without partial success |
| Pull output | Receiver preserves exposed pair, for example with `honor_labels: true` | Ineligible: accumulation and timing lose the snapshot |
| Remote Write 1.0 | Identity-eligible | Ineligible |
| Remote Write 2.0 input | Admit request entities independently | One pre-established source transaction per envelope; no cross-request metadata cache |
| Remote Write 2.0 output | Preserve tuple-derived labels | Preserve and atomically commit one envelope end to end |
| Direct OTLP | Resource/group partial success is permitted | Validate and commit one envelope without partial success |

A passive intermediary needs no gate but MUST be operator-attested. Composition-changing processors require
Option C recognition and envelope enforcement; generic batching is insufficient. Active boundaries require
atomic enforcement. Full is a closed-world deployment contract, not a wire property.

### Protocol Outcomes

**Scrape producer.** A valid target-info scalar may supply metadata; reject its exemplar because no OTLP point
owns it. Semantic failures emit diagnostics but do not rewrite scrape success or the source `up` value; `up`
still undergoes admission.

**Remote Write input (Option C producer).** Group before provisionally tallying every sample, histogram, and
exemplar once. `Written` retains its protocol meaning of data the receiver accepts; Option C does not require
durable storage. Finalize counts only after Resource materialization and applicable atomic validation. An accepted
target-info scalar counts once as a written sample even when a rejected sibling requires HTTP `400`, although
Option C consumes the scalar as Resource metadata; its independently rejected exemplar counts as zero. Rejection
of a logical series counts all its entities as zero and owns one diagnostic. Full atomic rejection counts every
request entity as zero. Validate before mutation. Partial semantic rejection returns permanent HTTP `400`;
version 2.0 reports exact counts and version 1.0 keeps its response rules.

**Direct OTLP core receiver.** Complete rejection returns non-retryable `InvalidArgument` or HTTP `400`;
partial success reports exact `rejected_data_points`.

**Atomic sender.** Validate the complete envelope before enqueueing or sending it.

**Atomic receiver.** Rejection writes nothing. Remote Write returns permanent HTTP `400` with zero version 2.0
counts. Direct OTLP returns non-retryable `InvalidArgument` or HTTP `400` without partial success. Transport and
storage failures use protocol retry rules.

## Rollout and Specification Status

Controls require standardization. Disabled gates use compatibility. Recognition-only activates tuples;
emission-only exposes controls without identity override. Together they activate
tuples and reject malformed ones. Atomic enforcement requires its gate.

Before recognition, inventory control collisions, fan-in, promotion, and tuple-changing processors. Before
full conformance, attest envelope boundaries, mapping, representation, limits, intermediaries, queues,
retries, and receiver atomicity. Payload contents establish none of these properties.

PromQL selects concrete `target_info`, not family `target`. Representation changes need review.

Until normative adoption, specifications govern. New markers, defaults, or canonical names require
separate standardization.
