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

# Translation Flows for Options A and B

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

Option C is authoritative when selected; the preceding A/B flow summary does not govern active tuples. It uses
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

| Mode | Successful-attempt guarantee |
| :---- | :---- |
| Core | Exact pair on accepted, supported, collision-free ordinary points and any canonical series; pull requires receiver preservation |
| Full | Core plus exact presence and byte-for-byte non-empty string value of each covered service attribute obtained from valid associated target metadata for every active Resource in each identity group of one locally accepted envelope |

These are round-trip guarantees: Core recovers the normalized pair, while Full additionally recovers the covered
metadata. Core is the base Option C behavior and supplies the scrape-identity guarantee. Full extends Core with
exact covered metadata and atomic rejection. Both guarantees apply only to conforming-producer active tuples;
activation cannot prove provenance. Invalid, unsupported, or excluded entities, incomplete or malformed identity,
external labels, receiver-added enrichment, explicitly promoted controls, and semantics-changing processors are
outside both guarantees. Full does not add guarantees for invalid, inactive, conflicting, or unassociated
metadata, target-info exemplars or target-info-only input, or metadata other than the covered service attributes.

Neither mode preserves source target-info presence, samples, timing, HELP, UNIT, start timestamps, exemplars,
representation, continuity, retirement, query-time uniqueness during label changes, delivery occurrence, or
delivery multiplicity. Static Full prerequisites, envelope rejection, and deployment conformance are specified
below rather than treated as guarantee exclusions.

### Terms and Translation Flow

| Term | Meaning |
| :---- | :---- |
| Normalized pair | Exact non-empty final `job` and `instance` label values after applicable relabeling, target filling, and label validation; Option C performs no further value rewriting and never derives either from `service.*` |
| Active tuple | Marker `"1"` and a normalized pair |
| Attempt | One scrape, pull response, OTLP/Remote Write request, or direct transaction |
| Identity group | Active Resources with one pair in an attempt |
| Ordinary survivor | Locally valid, final-mapped point retained by ordinary-only compatibility collision handling |
| Contributing Resource | Active Resource with a survivor; only survivor timestamps contribute |
| Canonical series | One physical target-info series with one label set and one or more scheduled samples |
| Canonical slot | Final target-info name plus pair in an attempt; with the pinned representation it fixes the metric-family collision footprint, while metadata and timestamps do not distinguish slots |
| Local acceptance | Boundary-local semantic acceptance and role action; Full performs one atomic action on the complete envelope, and neither mode implies downstream durable commit |
| Full mapping domain | Statically declared or derived set or predicate of original Resource keys available to canonical output for one path and mapping mode |

Emission and recognition are independent, disabled-by-default endpoint or pipeline gates. Atomic enforcement is
a third disabled-by-default option. Apply this processing model:

| Boundary and input | Core or compatibility behavior | Full delta |
| :---- | :---- | :---- |
| Enforcement enabled without that boundary's emission or recognition gate | Configuration error | Same |
| Producer emission disabled | Compatibility behavior; form no tuple | Enforcement cannot be enabled |
| Producer emission enabled | Admit source entities independently, then materialize active Resources | Pre-admission exclusions are not members; zero materialized active Resources form no envelope and make no Full-success claim; otherwise all materialized output forms one envelope |
| Consumer recognition disabled | Compatibility behavior; reserve no pair | Enforcement cannot be enabled |
| Recognition enabled; markerless Resource or wholly markerless presented unit | Compatibility behavior for each markerless Resource | A wholly markerless unit uses compatibility and makes no Full claim; in a marked unit, markerless Resources are envelope members and share its outcome |
| Recognition enabled; valid active tuple | Apply Core to that Resource | Any marker makes the complete presented unit one envelope; an invalid or unsupported metric entity in it rejects the envelope |
| Recognition enabled; invalid marker or marked tuple | Reject that Resource without fallback | Reject the complete marked envelope |

A consumer's OTLP point attributes and metadata cannot activate or supply controls; only Resource controls can.
A marker is present regardless of value or type. Protocol negotiation, decoding, and request-structure failures
that prevent construction of a source input or presented unit occur before envelope formation and use protocol
whole-request rules.

A producer uses three ordered phases:

1. **Finalize.** Negotiate and decode, structurally validate, apply relabeling, target filling, and label
   validation, extract the pair, classify reserved metadata, and assemble ordinary logical metric families.
2. **Admit.** Group by pair, resolve associated metadata within one scrape or complete Remote Write request, and
   decide each logical series once. Unsupported or incomplete metric entities and invalid, inactive, conflicting,
   or unassociated metadata and exemplars are pre-envelope exclusions; valid siblings continue.
3. **Materialize and account.** Construct Resources, atomically replace same-named controls, attach accepted
   metadata, form any non-empty Full envelope, validate it, and only then finalize counts and responses. Consumed
   metadata, exclusions, and identity point attributes are not emitted. Core may lose source boundaries.

The tuple supplies authoritative `job` and `instance` atomically to ordinary survivors and generated canonical
or noncanonical metadata series. It never supplies `service.*`; controls are not ordinary metadata or labels by
default.

Each semantic omission, rejection, or conflict MUST produce one implementation-defined **bounded diagnostic**
per affected source series, invalid Resource, pair/key conflict, or slot. Coalesce wire entities; series
rejection owns exemplar diagnostics. Finalize diagnostics only with the selected output branch; speculative
preflight or rebuild work adds none. Retries may repeat them.

Prometheus → OTLP:

| Scenario | Required Core behavior | Full delta |
| :---- | :---- | :---- |
| Complete pair; no target metadata | Store tuple; synthesize no `service.*` | Materialized Resources join the envelope with all covered attributes absent |
| Complete pair; valid target metadata | Store tuple and metadata; consume target-info | Materialized Resources join the envelope and covered attributes must satisfy its raw invariant |
| Service-looking ordinary label | Keep as point attribute | Same admission; any later ordinary collision rejects the formed envelope |
| Incomplete identity | Exclude entity; emit no partial tuple | Pre-envelope exclusion; valid siblings may still form an envelope |
| Invalid, conflicting, or unassociated metadata | Exclude invalid series or key; keep valid siblings | Pre-envelope exclusion outside the guarantee; valid siblings may still form an envelope |
| Target-info only | Exclude unassociated series; emit no empty `ResourceMetrics` | Form no envelope and make no Full-success claim |
| Emission disabled | Preserve compatibility behavior and responses | Enforcement is a configuration error |

OTLP → Prometheus:

| Scenario | Required Core behavior | Full delta |
| :---- | :---- | :---- |
| Recognition disabled | Use compatibility translation; reserve no pair | Enforcement is a configuration error |
| Recognition enabled; markerless, including service-only, bare-job, and point-label cases | Use compatibility translation; reserve no pair | A wholly markerless unit makes no Full claim; markerless Resources in a marked unit share its atomic outcome |
| Active tuple | Use pair as authoritative `job` and `instance` | The complete marked unit is one envelope |
| Invalid marked tuple | Reject Resource; never fall back | Reject the complete envelope |
| Covered service attributes | Include in enabled canonical series | Preserve identical presence and exact values across each active identity group or reject |
| Active Resources with and without ordinary survivors | Merge metadata and schedule only from contributing Resources | Raw covered invariants still inspect every active Resource; no contributor rejects output |
| Point or exporter identity conflict | Tuple wins atomically | Same; a resulting ordinary or canonical collision rejects the envelope |
| Canonical generation disabled, namespaced, or renamed | Follow the configured Core output state | Full is a configuration error |
| Occupied canonical slot or family collision | Use the transactional Core arbitration below | Reject the envelope |
| Explicit control promotion | Apply promotion; ordinary label set is outside both guarantees | Same; any resulting collision rejects the envelope |

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
request-wide without cross-request caching; request scope alone does not establish Full eligibility.

### Prometheus Output

Resolve output configuration before data:

| Output state | Core | Full |
| :---- | :---- | :---- |
| Standard canonical generation | Use the ordered algorithm below | Same, with Full rejection rules |
| Disabled generation | Retain ordinary survivors; generate no metadata series or canonical slot | Configuration error |
| Namespaced or renamed generation | Retain ordinary survivors and use compatibility name, representation, non-identity metadata mapping, schedule, and collisions; the tuple still supplies `job` and `instance` to the generated noncanonical series | Configuration error |

A generated noncanonical series has no Option C canonical slot and is outside both guarantees, but `service.*`
never replaces its tuple identity. Controls remain consumed unless explicitly promoted.

For standard generation, process each identity group in order. `S0` is the immutable baseline survivor set built
from the original locally valid points.

| Stage | Core | Full |
| :---- | :---- | :---- |
| 1. Ordinary survivors | Apply ordinary metric compatibility collision handling to build `S0` | Reject an ordinary collision; otherwise build `S0` |
| 2. Raw Resource merge | Merge contributing Resources by original key and raw value before conversion; absence is not a conflict, while disagreement, empty covered values, and non-string covered values omit that key | Reject a raw covered-invariant failure; then use Core merge-and-omit for non-covered contributor metadata |
| 3. Individual conversion and final names | Convert each retained key and value by compatibility rules; conversion failure omits that key; then apply Option C final-name rules | Reject a covered conversion, alias, reserved-name, or mapping-domain failure; omit the corresponding non-covered group |
| 4. Candidate preflight | If no final metadata label or contributor remains, or schedule, composition, or limit preflight fails, retain `S0` and emit no canonical series | With a contributor, no final metadata label produces pair-only canonical output; every other listed failure rejects the envelope |
| 5. Slot and family arbitration | Run the transactional collision procedure below | Any canonical-slot or metric-family collision rejects the envelope |
| 6. Commit | Emit the selected ordinary survivors and at most one canonical series only after the branch is stable | Perform the local atomic action only after complete-envelope validation |

Compatibility governs each individual Resource key-name and value conversion, but Option C replaces compatibility
collision concatenation for standard canonical Resource labels. One original key emits once. Distinct original
keys mapping to one final name omit that entire group under Core, even when values agree. A candidate mapping to
`job`, `instance`, `__name__`, or another protocol-reserved label loses to that use. Ordinary metric attributes
and compatibility-only noncanonical metadata retain compatibility collision behavior. Non-empty string
`service.name`, `service.namespace`, and `service.instance.id` remain canonical candidates regardless of
`keep_identifying_resource_attributes`; controls are excluded and the pair is added separately.

Core collision arbitration is transactional:

1. Fully preflight a provisional canonical candidate from `S0`. If compatibility family precedence makes it
   lose, retain `S0` and emit no canonical series.
2. The candidate owns its canonical slot. If winning arbitration excludes ordinary occupants, reserve its fixed
   slot and metric-family footprint. Rebuild survivors from the original locally valid points,
   excluding every reserved occupant before ordinary collision selection, and recompute contributors, metadata,
   labels, schedule, limits, and composition. If arbitration excludes nothing, select the candidate with `S0`.
3. If recomputation succeeds, select the rebuilt survivors and candidate. If it fails or leaves no contributor,
   discard the candidate and restore `S0`, including its occupants.

The fixed reservation prevents a rebuilt survivor from occupying it, so one rebuild suffices. Validate before
mutation. Composition-changing layers MUST rebuild from locally valid points and repeat this complete algorithm.
Each finalized omitted or rejected final-name group or slot owns one bounded runtime diagnostic; a statically
invalid mapping owns one configuration diagnostic.

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

Suffix-looking metrics remain ordinary unless they collide. Explicit `promote_resource_attributes`,
`promote_all_resource_attributes`, or equivalent promotion does not change tuple identity, but puts the
ordinary label set outside both guarantees.

Protocol rules govern staleness and discontinuity. A verified stale marker retiring prior canonical labels
is lifecycle output, not an active competitor; arbitrary stale input is not exempt. Info/Gauge changes are
metadata events and create no second concrete series with unchanged labels. Label changes use normal lifecycle
handling.

## Optional Full Mode

### Atomic Envelope and Eligibility

**Atomic-batch enforcement** is a third, disabled-by-default option, scoped to an input, endpoint, or
output and requiring its Option C gate. Payloads cannot request or prove it.

Envelope membership follows the processing table. A producer envelope is exactly its non-empty materialized
post-admission output; consumed metadata and exclusions are outside it. A consumer **presented unit** is one
request, batch, or invocation before semantic validation; any marker makes every Resource a member of one
envelope. Active Resources use Option C, while markerless Resources use compatibility and share the outcome.

A Full deployment MUST preserve one producer envelope as exactly one complete presented unit within each delivery
attempt at every downstream Full boundary. A retry may present that complete envelope again, but no attempt may
split, combine, remove, or partially commit members. A detectable boundary violation rejects the envelope; an
undetectable one makes the deployment nonconformant. Remote Write 2.0 input adds a deployment constraint: the
sender deliberately places exactly one intended atomic unit in each request, and the receiver forms at most one
post-admission envelope from that complete request. Pre-admission exclusions remain outside the envelope but
retain their protocol accounting. Cross-request assembly and metadata caching are prohibited. Remote Write
itself supplies neither this boundary nor transactionality; operators attest it.

Full requires these prerequisites and invariants before Resource conversion or merge-and-omit:

- **Raw covered attributes.** For each covered attribute (`service.name`, `service.namespace`, and
  `service.instance.id`) and each identity group in the atomic envelope, every active Resource in that group MUST
  have identical presence and, when present, one non-empty string value. Mismatch, empty or non-string values,
  or disagreement reject the envelope. A producer entity excluded before envelope formation is not a member
  and does not participate.
- **Mapping domain.** For each path and negotiated mapping mode, statically declare or derive the complete Full
  mapping domain. It covers every original key an active contributing Resource may supply to canonical output,
  including every key capable of aliasing a covered output. Controls and markerless compatibility-only Resources
  are outside this domain. The same domain, forward mapping, and inverse mapping MUST be operator-attested across
  the round-trip path; payloads cannot establish them.
- **Covered mapping.** Configuration validation MUST prove that every covered key maps reversibly and
  byte-preservingly, is injective against every domain key, and cannot become `job`, `instance`, `__name__`, or
  another canonical or protocol-reserved label. A statically possible violation is a configuration error.

Before conversion, a canonical metadata key outside the validated domain on an active contributing Resource
rejects the complete envelope. A covered alias detected after successful validation also rejects it and makes the
configuration, implementation, or deployment nonconformant. Default unconstrained dotted-to-underscore
conversion remains Core-only. Aliases involving only in-domain non-covered keys remain non-fatal omissions.

All covered attributes may be absent. After all invariants pass, non-covered metadata uses Core contributor
merge-and-omit behavior. Ordinary disagreement or omission of non-covered metadata does not itself reject Full.
Full emits after preflight and is pair-only exactly when no final canonical metadata label remains.

Before accepting Full, each active component MUST validate the locally knowable prerequisites for its role:

- the required emission or recognition gate and the ability to perform its local atomic action after semantic
  preflight;
- a Full-capable path; and
- for Prometheus output, standard canonical generation with one representation and a validated mapping domain.

A known failure is a configuration error, never data-time ineligibility or silent downgrade. The path table
identifies unsupported paths. Disabled atomic enforcement uses Core or compatibility behavior.

Local enforcement validates local properties. Full conformance also requires explicit operator attestation
of every non-local intermediary, queue, WAL, retry path, and receiver; without it the deployment MUST NOT
claim Full. Missing non-local attestation does not require a locally detectable startup failure. Payloads and
components do not infer attestation. After formation, an invalid or unsupported metric entity in any member,
invariant failure, ordinary or canonical collision, exceeded limit, missing schedule, invalid composition, or
permanent semantic failure rejects the complete envelope and prevents local acceptance. Specified in-domain
non-covered merge and conversion omissions remain non-fatal.

Each delivery or retry is a separate atomic attempt. Retries MUST preserve envelope membership and covered
attributes. Option C adds no request identity, deduplication, or exactly-once delivery semantics. A lost or
ambiguous acknowledgement may therefore replay the complete envelope. Transport and storage failures use
protocol retry rules; on each attempt the current boundary performs its local atomic action for all members or
none.

### Path Requirements and Attestation

| Path | Core behavior | Additional Full-mode requirement |
| :---- | :---- | :---- |
| Scrape producer | Emit active Resources from admitted ordinary entities | Form one post-admission scrape envelope |
| OTLP intermediary | Preserve active tuples; batching and partial success remain available | Preserve one envelope as one attested unit, including all members and covered attributes, without partial success |
| Pull output | Receiver preserves exposed pair, for example with `honor_labels: true` | Not Full-capable; enabling Full is a configuration error because accumulation and timing lose the snapshot |
| Remote Write 1.0 | Core-only | Not Full-capable; enabling Full is a configuration error |
| Remote Write 2.0 input | Admit request entities independently | Sender places one intended atomic unit in one request; receiver forms at most one post-admission envelope; no cross-request cache |
| Remote Write 2.0 output | Preserve tuple-derived labels | Carry one envelope per request; the receiver atomically commits it |
| Direct OTLP | Resource/group partial success is permitted | Treat one presented marked unit as the envelope; validate and commit it without partial success |

A passive intermediary needs no gate but MUST be operator-attested. Composition-changing processors require
Option C recognition and envelope enforcement; generic batching is insufficient. Active boundaries require
atomic enforcement. Full is a closed-world deployment contract, not a wire property.

### Protocol Outcomes

All responses and counts report local acceptance. Full is not a distributed transaction and adds no synchronous
end-to-end acknowledgement: downstream rejection of a complete envelope may remain conformant but cannot revise
an upstream response, Remote Write `Written` count, or other local accounting.

**Scrape producer.** A valid target-info scalar may supply metadata; reject its exemplar because no OTLP point
owns it. Semantic failures emit diagnostics but do not rewrite scrape success or the source `up` value; `up`
still undergoes admission.

**Remote Write input (Option C producer).** Group before provisionally tallying every sample, histogram, and
exemplar once, and finalize counts only after Resource materialization and applicable atomic validation. A
producer pre-admission exclusion counts all of that logical series' entities as zero and owns one diagnostic;
valid siblings may continue. An accepted target-info scalar counts once as a written sample even when a rejected
sibling requires HTTP `400`, although Option C consumes the scalar as Resource metadata; its independently
rejected exemplar counts as zero. A post-formation Full rejection makes every entity in the complete request
zero, including entities already excluded before envelope formation. Validate before mutation. Partial semantic
rejection returns permanent HTTP `400`; version 2.0 reports exact local `Written` counts and version 1.0 keeps its
response rules.

**Direct OTLP Core receiver.** Complete rejection returns non-retryable `InvalidArgument` or HTTP `400`;
partial success reports exact `rejected_data_points`.

**Atomic sender.** Validate the complete envelope before enqueueing or sending it.

**Atomic receiver.** Rejection performs no local atomic action. Remote Write returns permanent HTTP `400` with
zero version 2.0 `Written` counts for the request. Direct OTLP returns non-retryable `InvalidArgument` or HTTP
`400` without partial success. Transport and storage failures use protocol retry rules.

## Rollout and Specification Status

Controls and mapping-domain declarations require standardization. The processing table defines gate combinations,
compatibility behavior, and malformed-tuple rejection.

Before recognition, inventory control collisions, fan-in, promotion, and tuple-changing processors. Before
Full conformance, attest envelope boundaries, mapping, representation, limits, intermediaries, queues,
retries, and receiver atomicity. Payload contents establish none of these properties.

PromQL selects concrete `target_info`, not family `target`. Representation changes need review.

Until normative adoption, specifications govern. New markers, defaults, canonical names, or mapping mechanisms
require separate standardization.
