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

Option C is a standalone alternative that turns Option B's namespaced attribute names into a complete
contract. A producer stores scrape identity as the reserved Resource attributes `prometheus.job` and
`prometheus.instance`, and populates `service.name`, `service.namespace`, and `service.instance.id` only from
`target_info` — never from `job` or `instance`. A consumer translating OTLP to Prometheus treats a valid
reserved pair as authoritative for the `job` and `instance` labels. Resources without the pair keep today's
service.\*-derived translation unchanged, so Option C replaces the Options A/B translation flows above only
where it is active.

Deltas versus the Proposed Design above:

- Replaces the **Defaulting service.\* from job/instance** core rule and its opt-out toggle: covered
  attributes are never synthesized from scrape identity. When `target_info` does not supply them, they stay
  absent.
- The **Aggregated Exporter Fallback** reads the reserved pair instead of bare `job`/`instance` resource
  attributes; the service.\*-derived fallback for pair-less Resources is unchanged.
- Section 2's OTLP-endpoint `honor_labels` flag remains the compatibility mechanism for **bare**
  `job`/`instance` attributes (Option A). Option C needs no equivalent flag: the reserved names are new, so
  honoring them changes no existing traffic.

## Core Identity Contract

Unless overridden here, the [compatibility translation rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/),
[Prometheus exposition](https://prometheus.io/docs/instrumenting/exposition_formats/) and
[OpenMetrics](https://github.com/prometheus/OpenMetrics/blob/v1.0.0/specification/OpenMetrics.md) rules,
[Remote Write 1.0](https://prometheus.io/docs/specs/prw/remote_write_spec/) and
[2.0](https://prometheus.io/docs/specs/prw/remote_write_spec_2_0/) rules, and [OTLP](https://opentelemetry.io/docs/specs/otlp/)
[exporter rules](https://opentelemetry.io/docs/specs/otel/protocol/exporter/) apply.

| Term | Meaning |
| :---- | :---- |
| Producer | A Prometheus or OpenMetrics to OTLP translator that emits Option C attributes |
| Consumer | An OTLP to Prometheus translator that synthesizes Resource-level `job` and `instance` identity, such as Prometheus OTLP ingestion or an aggregated Prometheus exporter |
| Reserved pair | `prometheus.job` and `prometheus.instance`, both present as non-empty strings on one Resource |
| Normalized pair | The exact non-empty final `job` and `instance` label values after relabeling, target filling, and label validation; Option C performs no further value rewriting and never derives either value from `service.*` |
| Covered attributes | `service.name`, `service.namespace`, and `service.instance.id` |
| Translation unit | One scrape transaction or one received request or batch; `target_info` association never crosses units |
| Bounded diagnostic | At most one warning or error per affected series, Resource, or pair-and-key conflict per translation unit, not one per point |

Option C's semantics are defined per Resource and per translation unit. It preserves:

- the normalized pair, exactly: stored as the reserved pair on Prometheus → OTLP, and emitted verbatim as the
  `job` and `instance` labels on OTLP → Prometheus; and
- the covered attributes obtained from valid associated `target_info`, with exact presence and values, never
  dropped in favor of — or overwritten by — scrape identity.

It does not preserve the source `target_info` series itself: sample presence, timestamps, cadence, HELP,
UNIT, start timestamps, and exemplars are not represented. Receiver-added enrichment, external labels,
explicitly promoted reserved attributes, and semantics-changing processors are outside the contract.

Activation:

- Producer emission is a configuration opt-in and defaults to disabled (see Rollout).
- Consumer recognition is in-band: once the names are standardized, a complete reserved pair activates
  Option C for that Resource. Consumers MAY offer an opt-out setting that restores legacy translation.
- Only Resource attributes activate. Same-named data point attributes or metadata labels remain ordinary
  labels and never form or overwrite a pair.
- A partial, empty, or non-string pair does not activate: the consumer ignores both reserved values, derives
  `job` and `instance` through the complete legacy path, and reports one bounded diagnostic. One reserved
  value is never combined with one derived value.
- Recognition grants senders no new capability: any OTLP sender already fully controls `job` and `instance`
  through the covered attributes. The reserved names only make that intent explicit.

## Prometheus to OTLP

The producer finalizes labels under existing scrape rules (relabeling, `honor_labels` conflict handling,
scrape-target filling), groups ordinary points by the exact normalized pair, and associates `target_info`
within the translation unit. The pair is stored once per Resource; `job` and `instance` are not repeated as
point attributes.

| Scenario | Behavior |
| :---- | :---- |
| Complete pair; no target metadata | Store the reserved pair; leave covered attributes absent; synthesize no `service.*` |
| Complete pair; valid associated `target_info` | Store the reserved pair; convert `target_info` labels to Resource attributes; consume the series |
| Service-looking ordinary label | Keep as an ordinary point attribute; only `target_info` supplies covered attributes |
| `target_info` labels named `prometheus.job` or `prometheus.instance` | Ignore as metadata; they cannot overwrite the reserved pair |
| Identity incomplete after target filling | Fail that series with one bounded diagnostic; emit no partial pair |
| Invalid, conflicting, or unassociated `target_info` | Exclude the invalid series or key with one bounded diagnostic; valid siblings continue |
| `target_info` only | Consume it; emit no empty `ResourceMetrics` |
| Producer emission disabled | Complete existing translation; no reserved pair |

### Target metadata association

Recognition uses the final relabeled name. An exact `target_info` scalar with Gauge, Info, unknown, or no
type — including a Remote Write 2.0 scalar with Gauge, Info, or unset metadata — is usable metadata. Another
type or a histogram shape makes the series invalid reserved input. Suffix-looking names such as
`target_info_total` stay ordinary metrics, and type suffixes are never stripped.

Associate `target_info` with ordinary series sharing the exact pair in the same translation unit:

- Select each series' greatest-timestamp sample. A tie is valid only when all samples are stale or all are
  non-stale with value `1`; stale is inactive, and a non-stale value other than `1` invalidates the series.
- Remove the name and identity labels; convert the remaining labels by compatibility rules.
- Keep each supplied Resource key only when all supplying series agree; a conflicting key is omitted with one
  bounded diagnostic while unambiguous keys continue.
- Association is order-independent within the unit, with no cross-request caching.

## OTLP to Prometheus

| Scenario | Behavior |
| :---- | :---- |
| No reserved pair, or consumer opt-out configured | Complete legacy translation, including `keep_identifying_resource_attributes` behavior; no pair is reserved |
| Valid reserved pair | The pair is authoritative for `job` and `instance` on every ordinary metric and generated `target_info` for that Resource; neither value is derived from `service.*` |
| Partial, empty, or non-string pair | Legacy identity path with one bounded diagnostic; never mix a reserved value with a derived value |
| Pair plus conflicting point-level or exporter-added `job`/`instance` | The pair overwrites the conflicting identity; other point attributes keep existing handling |
| Point attributes named `prometheus.job` or `prometheus.instance` | Ordinary translated labels; they never activate Option C |
| Covered attributes present | Include them on generated `target_info` with exact values, regardless of `keep_identifying_resource_attributes`; on their own they justify generating `target_info` |
| Multiple Resources with the same pair in one unit | Each Resource translates independently per existing behavior; identical generated `target_info` label sets deduplicate, differing label sets remain distinct series exactly as with service.\*-derived identity today; no cross-Resource conflict detection |
| Reserved attribute explicitly promoted (`promote_resource_attributes`, or `promote_all_resource_attributes` minus `ignore_resource_attributes`) | Emit it under its translated name on ordinary series, not as `target_info` metadata; the resulting label set is outside the contract; identity handling is unchanged |
| `target_info` generation disabled or renamed | The setting remains authoritative; the pair still supplies `job` and `instance`; the covered-metadata part of the contract lapses (renamed output is covered only if the next producer recognizes it) |

Output rules:

- The reserved pair is consumed as identity: it is not emitted under its translated attribute names as
  ordinary labels or `target_info` metadata unless explicitly promoted.
- Generated `target_info` follows existing conventions — a value-`1` `target_info` Gauge, or OpenMetrics
  `target` Info where that representation is preserved — never both.
- Option C does not change `target_info` sample scheduling: Prometheus OTLP ingestion interpolates samples at
  half the query lookback delta between the earliest and latest sample timestamps, Remote Write export stamps
  the most recent timestamp, and pull exposition emits no explicit timestamp.
- Collisions between generated `target_info` and a real metric named `target_info`, and label-name collisions
  after translation, follow existing behavior; Option C adds no arbitration. Exact round-tripping of the
  dotted covered names requires a UTF-8-preserving translation strategy.
- PromQL matches the concrete `target_info` name, not the OpenMetrics family name `target`.

## Non-goals

- No cross-Resource or cross-request atomicity, batch envelopes, or consistency guarantees; intermediaries
  may split, merge, and batch freely.
- No delivery, deduplication, or exactly-once semantics; protocol retry rules are unchanged.
- No changes to protocol responses or accounting (partial success, Remote Write written counts, HTTP codes).
- No preservation of `target_info` sample timing, and no cross-Resource identity arbitration.
- Staleness and series lifecycle follow existing protocol rules.

## Requirements Mapping

- **Separate Storage**: satisfied by construction — the reserved pair and the covered attributes are distinct
  Resource attributes that never overwrite each other.
- **Universal Join Key**: the reserved pair when present; otherwise the existing service.\*-derived
  fallback, unchanged. When neither exists, output identity is as sparse as it is today — no regression, and
  no new claim.
- **Queryable Resource Attributes**: covered attributes are never dropped or rewritten, and appear on
  `target_info` regardless of `keep_identifying_resource_attributes`.
- **Non-Breaking Server Compatibility**: recognizing brand-new reserved names changes no existing traffic,
  and producer emission is opt-in, default off.

One consequence is deliberate: a target without `target_info` yields a Resource with **no `service.*` at
all**. Generic OTel consumers group such Resources as service-less rather than under a scrape-config-derived
name — per Practical Issue 3, an absent service identity is preferable to a polluted one. This requires the
compatibility specification to repeal, for Option C paths, its current rule that `service.name` and
`service.instance.id` MUST be filled on scrape.

## Comparison with Options A and B

| Aspect | Option A (bare) | Option B (namespaced) | Option C |
| :---- | :---- | :---- | :---- |
| Resource attributes | `job`, `instance` | `prometheus.job`, `prometheus.instance` | Same as B |
| Consumer activation | Requires the `honor_labels` server flag: bare names already occur in the wild | Unspecified | In-band on the reserved pair; optional opt-out |
| `service.*` defaulting from job/instance | Core Rules MAY-default plus toggle | Core Rules MAY-default plus toggle | Never |
| Breaking risk | Several flows marked BREAKING in the tables above | Low | None for existing traffic; producer emission opt-in |
| Collector / OTTL UX | Natural label names | Prefix must be learned | Prefix must be learned |
| Semantic-convention registration | Arguably none needed | Needed | Needed, as reserved names |

## Rollout

Consumer support ships first. Producer emission is gated behind an opt-in that defaults to disabled, because
emission is not backward compatible with consumers that do not recognize the pair:

| Producer | Producer emission | Consumer | Result |
| :---- | :---- | :---- | :---- |
| Existing producer (no pair) | Not applicable | Existing or Option C consumer | Complete legacy behavior |
| Option C producer | Disabled | Existing or Option C consumer | Complete legacy behavior |
| Option C producer | Enabled | Option C consumer | Option C contract |
| Option C producer | Enabled | Existing consumer | Unsupported; see below |

With a legacy consumer, an enabled producer fails in one of two ways:

- Without covered attributes (the target exposed no `target_info`), series translate with **no `job` or
  `instance` labels at all** and the pair is silently dropped — legacy consumers suppress `target_info`
  entirely when no identity label is derivable.
- With covered attributes, identity is **silently rewritten** to the service-derived `job`/`instance`, and
  the pair is demoted to escaped `prometheus_job`/`prometheus_instance` labels on `target_info`.

An operator therefore enables emission only after every downstream consumer that synthesizes Prometheus
identity supports Option C. Intermediaries need no changes: the reserved pair consists of ordinary Resource
attributes, so resource-keyed batching and aggregation preserve it by construction. Re-exposing through a
pull exporter requires stamping the pair as literal `job` and `instance` labels on all exposed series (new
behavior for pull exposers) and `honor_labels: true` on the downstream scraper, mirroring federation.

Standardization needs: register `prometheus.job` and `prometheus.instance` as reserved names in the semantic
conventions, and amend the compatibility specification — including the MUST-fill repeal above. Flipping the
producer default is a separate, later compatibility decision.

## Implementation Notes

Anchors as of current `main` in both repos:

- Collector `prometheusreceiver`: `internal/prom_to_otlp.go` (`CreateResource`) maps job → `service.name` and
  instance → `service.instance.id` today; under Option C it stores the reserved pair instead and leaves
  covered attributes to `target_info`. `internal/transaction.go` (`AddTargetInfo`) already consumes
  `target_info` into Resource attributes and skips its `job`/`instance` labels; it additionally ignores
  reserved-name labels. Identity completion already falls back to scrape-target context
  (`getJobAndInstance`, `transaction.go:558`).
- Collector `pkg/translator/prometheusremotewrite` (`createAttributes`, `helper.go`, v1 and v2 paths): check
  the reserved pair before the hard-coded service.\* → `job`/`instance` derivation. Contrib currently lacks
  Prometheus's `keep_identifying_resource_attributes`/`promote_resource_attributes` knobs.
- Collector `prometheusexporter` (`extractJob`/`extractInstance`, `utils.go`): same reserved-pair check, plus
  the new pull-output rule of stamping the pair on all exposed series, which today carry no `job`/`instance`
  labels outside `target_info`.
- Prometheus OTLP endpoint (`storage/remote/otlptranslator/prometheusremotewrite`): the reserved-pair check
  slots in before the service.\* derivation in `setResourceContext` (`metrics_to_prw.go:443`). The translator
  already questions today's behavior — `helper.go:93`: "XXX: Should we always drop service namespace/service
  name/service instance ID from the labels" — which is the ambiguity Option C resolves. Covered attributes
  stop being excluded from `target_info` and count toward the non-identifying-attribute check that decides
  whether `target_info` is generated at all (`helper.go:544-554`).

## Open Questions

- Venue and process for registering the reserved names (semantic-conventions registry vs. compatibility
  specification only).
- When, if ever, producer emission flips to default-on — a major-version decision, aligned with Section 2's
  default flips.
- Whether the contrib Remote Write translator should adopt upstream Prometheus's
  `keep_identifying_resource_attributes` and `promote_resource_attributes` for parity.
- Whether consumers recognize renamed `target_info` output for covered-metadata purposes.
- Precedence if PR 4956's bare `job`/`instance` resource attributes proceed independently: suggested rule — a
  valid reserved pair wins over bare attributes, and sources are never mixed.
