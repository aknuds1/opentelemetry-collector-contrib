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
| Full | Core plus exact presence and byte-for-byte non-empty string value of each covered service attribute obtained from valid associated target metadata for every active Resource in each identity group of one committed envelope |

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
| Canonical slot | Final target-info name plus pair in an attempt; metadata and timestamps do not distinguish slots |

Emission and recognition are independent, disabled-by-default endpoint or pipeline options. Recognition disabled
or markerless uses compatibility translation without reserving the pair. With recognition enabled, marker `"1"`
plus a complete pair activates. When Full enforcement is disabled, other present marker values or malformed
marked pairs reject that Resource under Core without fallback. Full envelope activation and rejection are
specified below. Point attributes and metadata cannot activate or supply controls.

A producer uses three ordered phases:

1. **Source finalization.** Negotiate and decode the protocol, perform structural validation, apply applicable
   relabeling, target filling, and label validation, and extract the normalized pair. Classify reserved target
   metadata as specified below before grouping. Independently assemble only ordinary metric fragments into
   logical histograms, summaries, and other families under compatibility and protocol rules.
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

The tuple supplies authoritative `job` and `instance` atomically to ordinary survivors and canonical series. It
never supplies `service.*`; controls are not ordinary metadata or labels by default.

Each semantic omission, rejection, or conflict MUST produce one implementation-defined **bounded diagnostic**
per affected source series, invalid Resource, pair/key conflict, or slot. Coalesce wire entities; series
rejection owns exemplar diagnostics. Retries may repeat them.

Prometheus → OTLP:

| Scenario | Required Core behavior |
| :---- | :---- |
| Complete pair; no target metadata | Store tuple; synthesize no `service.*` |
| Complete pair; valid target metadata | Store tuple and metadata; consume target-info |
| Service-looking ordinary label | Keep as point attribute |
| Incomplete identity | Exclude entity; emit no partial tuple |
| Invalid, conflicting, or unassociated metadata | Exclude invalid series or key; keep valid siblings |
| Target-info only | Exclude unassociated series; emit no empty `ResourceMetrics` |
| Emission disabled | Preserve compatibility behavior and responses |

OTLP → Prometheus:

| Scenario | Required Core behavior |
| :---- | :---- |
| Recognition disabled or markerless, including service-only, bare-job, and point-label cases | Use compatibility translation; reserve no pair |
| Active tuple | Use pair as authoritative `job` and `instance` |
| Invalid marked tuple | Reject Resource; never fall back |
| Covered service attributes | Include in enabled canonical series |
| Active Resources with and without ordinary survivors | Merge canonical metadata and schedule only from contributing Resources |
| Point or exporter identity conflict | Tuple wins atomically |
| Disabled, namespaced, or renamed canonical generation | Follow Core configuration; enabling Full is a configuration error |
| Occupied canonical slot | Canonical owns slot under Core; Full rejects |
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
request-wide without cross-request caching; request scope alone does not establish Full eligibility.

### Prometheus Output

Resolve static mode and output configuration before processing data. If canonical generation is disabled,
namespaced, or renamed, Core follows that configuration, retains ordinary survivors, reserves no canonical slot,
and stops the Option C canonical branch. A Full-enabled configuration with any of those settings is invalid.

With valid static configuration, use this ordered pipeline for each identity group:

1. Build locally valid ordinary points with tuple identity, apply ordinary-only compatibility collision handling,
   and obtain ordinary survivors. Full also applies its raw covered-attribute and covered-mapping preflight below.
2. Merge and map Resource attributes only from contributing Resources. Keep a final label when all contributing
   Resources that contain its source key agree; absence is not a conflict. Non-covered disagreements use the same
   Core merge-and-omit behavior in both modes. Non-empty string `service.name`, `service.namespace`, and
   `service.instance.id` remain eligible for canonical output regardless of
   `keep_identifying_resource_attributes`. Exclude controls and add the pair separately.
3. Determine whether canonical output is needed, build the path schedule, and preflight limits and final
   composition.
4. Arbitrate the canonical slot and metric-family collisions.
5. Emit exactly one canonical series only after every applicable step succeeds.

Evaluate the conditions below in pipeline order. Only **stop** and **reject** are terminal; **continue** advances
to the next condition.

| Condition | Core | Full |
| :---- | :---- | :---- |
| Ordinary collision | Keep compatibility-selected survivors; continue | Reject the envelope |
| Raw covered attributes differ in presence or value, or are empty or non-string | Continue with Core merge-and-omit behavior | Reject the envelope |
| Envelope-specific covered conversion failure or inability to validate final composition | Retain ordinary survivors, including compatibility-translated `target_info`; reserve no canonical slot and stop | Reject the envelope |
| No final canonical metadata label | Retain ordinary `target_info` compatibility behavior; reserve no canonical slot and stop | Continue with pair-only canonical output |
| No contributing Resource, missing path-required usable timestamp, unusable schedule, exceeded hard limit, or envelope-specific preflight failure | Retain ordinary survivors; reserve no canonical slot and stop | Reject the envelope |
| Occupied canonical slot or metric-family collision | Canonical output owns its slot and omits every occupant; other collisions use compatibility behavior; continue when resolved | Reject the envelope |
| Pipeline succeeds | Emit exactly one canonical series | Emit exactly one canonical series |

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

Validate before mutation. Composition-changing layers MUST rebuild ordinary survivors from locally valid points
and repeat the pipeline. Suffix-looking metrics remain ordinary unless they collide.

Resource-to-label conversion uses compatibility rules. Explicit `promote_resource_attributes`,
`promote_all_resource_attributes`, or equivalent promotion does not change tuple identity, but puts the
ordinary label set outside both guarantees. Full's additional mapping requirements are defined below.

Protocol rules govern staleness and discontinuity. A verified stale marker retiring prior canonical labels
is lifecycle output, not an active competitor; arbitrary stale input is not exempt. Info/Gauge changes are
metadata events and create no second concrete series with unchanged labels. Label changes use normal lifecycle
handling.

## Optional Full Mode

### Atomic Envelope and Eligibility

**Atomic-batch enforcement** is a third, disabled-by-default option, scoped to an input, endpoint, or
output and requiring its Option C gate. Payloads cannot request or prove it.

Full distinguishes producer formation from consumer activation:

- An enabled producer performs source admission, materializes its post-admission OTLP output, and forms that
  complete output into one **atomic envelope**. Consumed source metadata and pre-admission exclusions are not
  members. Only source admission before envelope formation may be partial.
- At a consumer with recognition and atomic enforcement enabled, a **presented unit** is one request, batch, or
  invocation delivered to that enforcement boundary before semantic validation. If no Resource contains the
  `prometheus.scrape.identity.version` key, the consumer processes the complete unit through compatibility
  behavior and makes no Full claim for that attempt. If any Resource contains the key, the complete presented
  unit is one atomic envelope, including every marked and markerless Resource. Marker presence is independent of
  its value or type: an invalid marker or malformed marked tuple rejects the complete envelope. Active Resources
  use Option C, while markerless Resources use compatibility translation but share the atomic outcome.

A Full deployment MUST preserve one producer envelope as exactly one complete presented unit within each delivery
attempt at every downstream Full boundary. A retry may present that complete envelope again, but no attempt may
split, combine, remove, or partially commit members. A detectable boundary violation rejects the envelope; an
undetectable one makes the deployment nonconformant. Remote Write 2.0 input adds a deployment constraint: the
sender deliberately places exactly one intended atomic unit in each request, and the receiver forms at most one
post-admission envelope from that complete request. Pre-admission exclusions remain outside the envelope but
retain their protocol accounting. Cross-request assembly and metadata caching are prohibited. Remote Write
itself supplies neither this boundary nor transactionality; operators attest it.

Full applies two invariants before Resource conversion or merge-and-omit:

- **Raw covered attributes.** For each covered attribute (`service.name`, `service.namespace`, and
  `service.instance.id`) and each identity group in the atomic envelope, every active Resource in that group MUST
  have identical presence and, when present, one non-empty string value. Mismatch, empty or non-string values,
  or disagreement reject the envelope. A producer entity excluded before envelope formation is not a member
  and does not participate.
- **Covered mapping.** The attested forward and reverse path MUST recover every present covered Resource key
  exactly and preserve its string value byte-for-byte. Final label-name conversion MUST be provably reversible
  and injective for covered outputs relative to every Resource key the path can emit. A covered final label MUST
  NOT collide with another emitted canonical label, `job`, `instance`, `__name__`, or another protocol-reserved
  label. An envelope-dependent violation rejects that envelope before merge or mutation.

All covered attributes may be absent. After both invariants pass, non-covered metadata uses Core contributor
merge-and-omit behavior. Ordinary disagreement or omission of non-covered metadata does not itself reject Full.
Full emits after preflight and is pair-only exactly when no final canonical metadata label remains.

Before accepting a Full-enabled configuration, each active component MUST validate every locally knowable
prerequisite relevant to its role: its required emission or recognition gate; local semantic preflight and the
role-appropriate ability to atomically emit, preserve, or commit one envelope; a Full-capable path; and, where it
produces Prometheus output, canonical generation enabled with one unrenamed, unnamespaced representation and a
reversible, injective covered mapping. Static inability is a configuration error, never a data-time ineligibility
result or silent downgrade. Pull output and Remote Write 1.0 are not Full-capable. Default dotted-to-underscore
conversion is Core-only because it is not injective relative to every Resource key the path can emit. Disabled
atomic enforcement uses Core or compatibility behavior.

Local enforcement validates local properties. Full conformance also requires explicit operator attestation
of every non-local intermediary, queue, WAL, retry path, and receiver; without it the deployment MUST NOT
claim Full. Missing non-local attestation does not require a locally detectable startup failure. Payloads and
components do not infer attestation. After formation, invalid content in any member, covered invariant or mapping
failures, ordinary or canonical collisions, exceeded limits, missing path-required schedules, inability to
validate final composition, or permanent semantic failure at the atomic boundary reject the complete envelope
and commit nothing.

Each delivery or retry is a separate atomic attempt. Retries MUST preserve envelope membership and covered
attributes. Option C adds no request identity, deduplication, exactly-once delivery, or end-to-end acknowledgement
semantics. A lost or ambiguous acknowledgement may therefore replay the complete envelope. Transport and
storage failures use protocol retry rules; every attempt still commits all members or none.

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

**Direct OTLP Core receiver.** Complete rejection returns non-retryable `InvalidArgument` or HTTP `400`;
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
Full conformance, attest envelope boundaries, mapping, representation, limits, intermediaries, queues,
retries, and receiver atomicity. Payload contents establish none of these properties.

PromQL selects concrete `target_info`, not family `target`. Representation changes need review.

Until normative adoption, specifications govern. New markers, defaults, or canonical names require
separate standardization.
