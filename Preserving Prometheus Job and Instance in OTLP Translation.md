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
  `job`/`instance` attributes (Option A). Option C needs no equivalent flag: no known existing traffic uses
  the reserved names, and traffic that does changes translation on consumer upgrade, bounded by the consumer
  opt-out.

## Core Identity Contract

Unless overridden here, the existing [Prometheus–OpenMetrics compatibility rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/)
and the underlying exposition, OpenMetrics, Remote Write, and OTLP specifications apply.

| Term | Meaning |
| :---- | :---- |
| Producer | A Prometheus or OpenMetrics to OTLP translator that emits Option C attributes |
| Consumer | An OTLP to Prometheus translator that synthesizes Resource-level `job` and `instance` identity, such as Prometheus OTLP ingestion or an aggregated Prometheus exporter |
| Reserved pair | `prometheus.job` and `prometheus.instance`, both present as non-empty strings on one Resource |
| Normalized pair | The final `job` and `instance` label values after relabeling, target filling (filling `job`/`instance` from the scrape-target configuration), and label validation, both non-empty |
| Covered attributes | `service.name`, `service.namespace`, and `service.instance.id` |
| Translation unit | One scrape transaction, one received request or batch, or — for pull exposition — one exposition scrape over the accumulated state |
| Legacy translation | Today's translation behavior, unmodified by Option C |
| Bounded diagnostic | At most one warning or error per affected series or Resource per translation unit, never one per data point |

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
- A partial, empty, or non-string pair does not activate: the consumer ignores both reserved values for
  identity, derives `job` and `instance` through legacy translation, and reports one bounded diagnostic. One
  reserved value is never combined with one derived value.
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
| Invalid or conflicting `target_info` | Exclude the invalid series or key with one bounded diagnostic; valid siblings continue |
| `target_info` whose pair matches no ordinary series in the unit | Consume it without diagnostic; it produces no output and no empty `ResourceMetrics`, regardless of what else the unit contains |
| Producer emission disabled | Unchanged legacy translation; no reserved pair emitted |

### Target metadata association

Recognition uses the final relabeled name. A series named exactly `target_info`, with scalar samples and
Gauge, Info, unknown, or no type — for Remote Write 2.0, with Gauge, Info, or unset metadata — is usable
target metadata. Any other type, or a histogram shape, makes the series invalid target metadata, handled per
the invalid row above. Suffix-looking names such as `target_info_total` stay ordinary metrics, and type
suffixes are never stripped.

Associate `target_info` with ordinary series sharing the same normalized pair in the same translation unit:

- For each series, use the sample with the greatest timestamp. If several samples tie for greatest timestamp,
  the tie is valid only when they are all stale markers or all non-stale with value `1`. Only the selected
  sample's staleness and value are examined: a stale selected sample means the series is inactive and
  contributes no attributes, and a non-stale selected sample with a value other than `1` makes the series
  invalid.
- Remove the name and identity labels; convert the remaining labels by compatibility rules. In escaped
  exposition, labels named exactly `service_name`, `service_namespace`, or `service_instance_id` populate the
  corresponding covered attributes; no other label is ever un-escaped. (Escaped exposition is the dominant
  wire format for SDK-exposed `target_info`; without this rule, Option C would not solve Practical Issue 1
  for it.)
- Keep each supplied Resource key only when all supplying series agree; a conflicting key is omitted with one
  bounded diagnostic while unambiguous keys continue.
- Association is order-independent within the unit. Scrape association never crosses translation units;
  push-protocol producers MAY carry per-pair association state across requests — as the contrib Remote Write
  receiver does today — with newer `target_info` replacing older attributes per key.

## OTLP to Prometheus

| Scenario | Behavior |
| :---- | :---- |
| Neither reserved attribute present, or consumer opt-out configured | Unchanged legacy translation, including `keep_identifying_resource_attributes` behavior; no pair is reserved |
| Valid reserved pair | The pair is authoritative for `job` and `instance` on every ordinary metric and generated `target_info` for that Resource; neither value is derived from `service.*` |
| Any other combination: one reserved attribute present, or either value empty or non-string | Legacy translation for identity with one bounded diagnostic; never mix a reserved value with a derived value; the invalid reserved attributes are then handled as ordinary resource attributes |
| Pair plus conflicting point-level or exporter-added `job`/`instance` | The pair overwrites the conflicting identity; other point attributes keep existing handling |
| Point attributes named `prometheus.job` or `prometheus.instance` | Ordinary translated labels; they never activate Option C |
| Covered attributes present | Include them on generated `target_info` with exact values, regardless of `keep_identifying_resource_attributes`; on their own they justify generating `target_info`. Empty or non-string covered values follow existing translation behavior and are outside the contract |
| Multiple Resources with the same pair in one unit | Each Resource translates independently; collapse and collisions follow each consumer's existing behavior (see note below); no cross-Resource conflict detection |
| Reserved attribute explicitly promoted (`promote_resource_attributes`, or `promote_all_resource_attributes` minus `ignore_resource_attributes` — server settings that copy resource attributes onto ordinary series) | Emit it under its translated name on ordinary series, not as `target_info` metadata; identity handling is unchanged (see note below) |
| `target_info` generation disabled or renamed | The setting remains authoritative; the pair still supplies `job` and `instance`; the covered-metadata part of the contract lapses (renamed output is covered only if the next producer recognizes it — provisional; see Open Questions) |

Note: same-pair fan-in today means exact label-set-and-timestamp deduplication in Prometheus OTLP ingestion,
distinct series in Remote Write export, and a single first-wins attribute set per pair in the aggregated pull
exporter. Promoted label sets are outside the contract.

Output rules:

- The reserved pair is consumed as identity: it is not emitted under its translated attribute names as
  ordinary labels or `target_info` metadata unless explicitly promoted.
- Generated `target_info` follows existing conventions — a value-`1` `target_info` Gauge, or OpenMetrics
  `target` Info where that representation is preserved — never both.
- Option C does not change `target_info` sample scheduling: ingestion interpolation, Remote Write
  timestamping, and timestamp-less pull exposition all keep their existing behavior.
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
- **Queryable Resource Attributes**: for Resources carrying a valid reserved pair, covered attributes are
  never dropped or rewritten and appear on `target_info` regardless of
  `keep_identifying_resource_attributes`; pair-less traffic keeps today's behavior (see Section 2).
- **Non-Breaking Server Compatibility**: no known existing traffic uses the reserved names (traffic that does
  changes translation on consumer upgrade, bounded by the consumer opt-out), and producer emission is opt-in,
  default off.

One consequence is deliberate: with producer emission enabled, a target without `target_info` yields a
Resource with **no `service.*` at all**. Generic OTel consumers group such Resources as service-less rather
than under a scrape-config-derived name — per Practical Issue 3, an absent service identity is preferable to
a polluted one. Operators who prefer job-derived service names can still create them deliberately — e.g. an
OTTL statement such as `set(resource.attributes["service.name"], resource.attributes["prometheus.job"])` —
turning the derivation into an explicit per-pipeline choice rather than a default; such a processor is
semantics-changing and intentionally outside the contract. This requires the compatibility specification to
repeal, for Option C paths, its current rule that `service.name` and `service.instance.id` MUST be filled on
scrape.

## Comparison with Options A and B

| Aspect | Option A (bare) | Option B (namespaced) | Option C |
| :---- | :---- | :---- | :---- |
| Resource attributes | `job`, `instance` | `prometheus.job`, `prometheus.instance` | Same as B |
| Consumer activation | Requires the `honor_labels` server flag: bare names already occur in the wild and carry no provenance — a consumer cannot distinguish scrape identity from an arbitrary attribute named `job` | Unspecified | In-band on the reserved pair; optional opt-out |
| `service.*` defaulting from job/instance | Core Rules MAY-default plus toggle | Core Rules MAY-default plus toggle | Never |
| Breaking risk | Several flows marked BREAKING in the tables above | Low | No known affected traffic; misordered rollout is unsafe (see Rollout) |
| Collector / OTTL UX | Natural label names | Prefix must be learned | Prefix must be learned |
| Semantic-convention registration | Arguably none needed | Needed | Needed, as reserved names |

## Rollout

Consumer support ships first. Producer emission is gated behind an opt-in that defaults to disabled, because
emission is not backward compatible with consumers that do not recognize the pair:

| Producer | Producer emission | Consumer | Result |
| :---- | :---- | :---- | :---- |
| Existing producer (no pair) | Not applicable | Existing or Option C consumer | Unchanged legacy translation |
| Option C producer | Disabled | Existing or Option C consumer | Unchanged legacy translation |
| Option C producer | Enabled | Option C consumer | Option C contract |
| Option C producer | Enabled | Existing consumer | Unsupported; see below |

With a legacy consumer, an enabled producer fails in one of two ways:

- Without covered attributes (the target exposed no `target_info`), series translate with **no `job` or
  `instance` labels at all** and the pair is silently dropped (absent promotion settings) — legacy consumers
  suppress `target_info` entirely when no identity label is derivable.
- With covered attributes, identity is **silently rewritten** to the service-derived `job`/`instance`, and
  the pair is demoted to escaped `prometheus_job`/`prometheus_instance` labels on `target_info`.

An operator therefore enables emission only after every downstream consumer that synthesizes Prometheus
identity supports Option C. Intermediaries need no changes: the reserved pair consists of ordinary Resource
attributes, so resource-keyed batching and aggregation preserve it by construction. Re-exposing through a
pull exporter also keeps working: the exporter already stamps derived `job` and `instance` labels on all
exposed series, and under Option C that stamping reads the reserved pair; the downstream scraper preserves
them with `honor_labels: true`, mirroring federation.

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
  (`getJobAndInstance` in `internal/transaction.go`).
- Collector `pkg/translator/prometheusremotewrite` (`createAttributes`, `helper.go`, v1 and v2 paths): check
  the reserved pair before the hard-coded service.\* → `job`/`instance` derivation. Contrib currently lacks
  Prometheus's `keep_identifying_resource_attributes`/`promote_resource_attributes` knobs.
- Collector `prometheusexporter` (`extractJob`/`extractInstance` in `utils.go`): same reserved-pair check.
  The exporter already stamps derived `job`/`instance` on all exposed series (`getMetricMetadata` in
  `collector.go`); under Option C that stamping reads the reserved pair.
- Prometheus OTLP endpoint (`storage/remote/otlptranslator/prometheusremotewrite`): the reserved-pair check
  slots in before the service.\* derivation in `setResourceContext` (`metrics_to_prw.go`). The translator
  already questions today's behavior — `helper.go`: "XXX: Should we always drop service namespace/service
  name/service instance ID from the labels" — which is the ambiguity Option C resolves. Covered attributes
  stop being excluded from `target_info` and count toward the non-identifying-attribute check that decides
  whether `target_info` is generated at all (`addResourceTargetInfo` in `helper.go`).

## Open Questions

- Venue and process for registering the reserved names (semantic-conventions registry vs. compatibility
  specification only).
- When, if ever, producer emission flips to default-on — a major-version decision, aligned with Section 2's
  default flips.
- Whether the contrib Remote Write translator should adopt upstream Prometheus's
  `keep_identifying_resource_attributes` and `promote_resource_attributes` for parity.
- Whether consumers recognize renamed `target_info` output for covered-metadata purposes.
- Standardized retention and eviction behavior for push-producer cross-request association state.
- Spec PR 4956 (bare `job`/`instance` resource attributes) is not accepted by Prometheus maintainers,
  over the assumption that bare names carry Prometheus provenance — the objection Option C's namespacing answers. Should a bare-name
  mapping be revived, the suggested precedence rule stands: a valid reserved pair wins over bare attributes,
  and sources are never mixed.
