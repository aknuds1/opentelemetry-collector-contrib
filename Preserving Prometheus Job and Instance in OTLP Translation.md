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
- `prometheus.job` and `prometheus.instance` are reserved Resource attributes.
  Together they form the **reserved pair**. A consumer MUST apply Option C to
  all metric points associated with a Resource when both attributes are present
  on that Resource as non-empty strings.
- Same-named metric data point attributes do not cause Option C to apply and
  remain ordinary metric attributes.
- Reserved Resource attributes are consumed as translation identity. Whether
  the pair is valid or malformed, they are not emitted by default under their
  translated attribute names as `target_info` metadata labels or ordinary
  metric labels.
- The **covered service attributes** are `service.name`, `service.namespace`,
  and `service.instance.id`. The round-trip guarantee covers an attribute only
  when its value comes from valid associated `target_info` and is a non-empty
  string.
- A **supported ordinary metric point** is a non-`target_info` point whose
  metric type, value, and timestamp both translators otherwise accept. Option C
  changes its identity and metadata translation, not metric-type support.
- A **translation unit** is one scrape transaction or one received Prometheus
  batch or request. `target_info` association is limited to that unit; Option C
  does not retain target metadata for later units.
- A **bounded diagnostic** means at most one warning or error for the specified
  series, Resource, or identity-and-key combination in a translation unit, not
  one diagnostic per metric point.

## Prometheus to OTLP

- For each source series, first use its final `job` and `instance` label values
  after scrape label conflict handling and metric relabeling. If either value is
  empty, fill only that missing value from scrape-target context when available.
  Do not otherwise derive or rewrite either value.
- If either value is still empty, fail that ordinary series, report one bounded
  diagnostic for it, and emit neither a partial reserved pair nor partial OTLP
  output for the series.
- Group supported ordinary metric points by the resulting exact pair. Store the
  values as `prometheus.job` and `prometheus.instance` Resource attributes, and
  do not also store the source `job` or `instance` as metric data point
  attributes.
- Associate `target_info` only by the same exact normalized pair within the
  translation unit. An incomplete `target_info` identity cannot associate and
  produces a bounded diagnostic.
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
- Labels named `prometheus.job` or `prometheus.instance` on `target_info` are
  ignored as metadata and cannot overwrite the reserved pair. Other target
  metadata retains its existing handling but is outside the guarantee.
- Consume `target_info` rather than translating it as an OTLP metric. A
  translation unit containing only `target_info` emits no empty
  `ResourceMetrics`.

## OTLP to Prometheus

- When a valid reserved pair is present, use `prometheus.job` and
  `prometheus.instance` atomically as the `job` and `instance` labels on every
  ordinary metric and generated `target_info` for that Resource. The pair is
  authoritative over conflicting point-level or exporter-added identity, and
  neither value is derived from `service.*`.
- When `target_info` generation is enabled, include every present covered
  service attribute with a non-empty string value on `target_info`, regardless
  of `keep_identifying_resource_attributes`. Generate `target_info` when these
  are the only metadata attributes. A valid reserved pair alone does not
  require `target_info`.
- Empty or non-string covered service attributes follow existing translation
  behavior but are outside the round-trip guarantee.
- When neither reserved attribute is present, preserve the existing
  service-derived identity and configuration behavior.
- When the pair is partial, empty, or non-string, report one bounded diagnostic
  for the Resource, ignore both reserved values for identity, and derive both
  identity labels through the complete legacy identity path. Preserve legacy
  handling for non-reserved metadata, but never combine one reserved identity
  value with one legacy value.
- Settings that disable `target_info` remain authoritative. They do not affect
  the reserved `job` and `instance` labels, but the service-metadata part of the
  guarantee no longer applies. A setting that renames `target_info` is covered
  only when the receiving producer recognizes the renamed series as
  `target_info`.
- Resource-to-ordinary-label conversion remains orthogonal. This includes
  `promote_resource_attributes`, `promote_all_resource_attributes`, and
  equivalent exporter settings. Either reserved attribute may be explicitly
  promoted according to the existing include, ignore, and collision rules.
  Such promotion emits the attribute under its translated name on ordinary
  metric series, not as additional `target_info` metadata. A resulting label
  set that promotes either reserved attribute is outside the round-trip
  guarantee.
- Label-name translation still applies to generated `target_info`. Exact
  service-attribute round-tripping requires the mapping to preserve each
  covered dotted name and to be injective across all labels generated for that
  `target_info` series. A UTF-8-preserving, no-translation strategy avoids
  normalization-induced loss and collisions. If a key is renamed or collides,
  that covered service attribute is outside the guarantee; reserved scrape
  identity remains covered.

## Translation Scenarios

The tables below summarize the Option C rules above; those rules remain
authoritative. They do not apply to the earlier Summary of Translation Flows.
Here, `service.*` means any individually present subset of the covered service
attributes, and the legacy path means the complete existing translation after
reserved Resource attributes have been filtered. Rows whose conditions occur
together are read together.

### Prometheus to OTLP

| Prometheus input | OTLP Resource attributes | OTLP metric data point attributes and guarantee |
| :---- | :---- | :---- |
| Complete normalized `job` / `instance`, without associated `target_info` | Stored as `prometheus.job` / `prometheus.instance`; covered `service.*` is absent | Scrape `job` / `instance` is not repeated; other ordinary metric labels remain point attributes |
| Complete normalized identity and active associated `target_info` containing unambiguous, non-empty `service.*` | Reserved pair plus exactly those covered `service.*` values | Same point handling as above; `target_info` metric and sample timing are not represented |
| Several active associated `target_info` series agree on a covered key | The single agreed value is stored | Presence on one or several source series is not distinguished |
| Several active associated `target_info` series disagree on one covered key | The conflicting key is omitted; other unambiguous covered keys are retained | One bounded diagnostic is reported for the conflicting key and identity pair |
| Active associated `target_info` has an empty covered value | That covered key is absent unless another active series supplies one unambiguous non-empty value | The empty value is outside the guarantee |
| Ordinary metric labels named `service.*`, `prometheus.job`, or `prometheus.instance` | They do not populate or overwrite Resource identity or service metadata | They remain ordinary point attributes; only associated `target_info` supplies covered service Resource attributes |
| `target_info` labels named `prometheus.job` or `prometheus.instance` | The normalized scrape pair remains authoritative; conflicting metadata cannot overwrite it | Conflicting reserved-name metadata is outside the guarantee; independently valid covered service metadata follows the rule above |
| Associated `target_info` has no samples or its selected state is stale | The reserved pair remains authoritative; that series contributes no metadata | Earlier samples are not used as fallback |
| Selected `target_info` state is malformed or has a conflicting greatest-timestamp tie | The reserved pair remains authoritative; that series contributes no metadata | One bounded diagnostic is reported; its metadata is outside the guarantee |
| Ordinary series identity remains incomplete after target-context filling | Nothing is emitted for that series | The series fails with a bounded diagnostic; no partial reserved pair is emitted |
| `target_info` identity remains incomplete after target-context filling | It cannot associate or contribute metadata | One bounded diagnostic is reported |
| Recognized `target_info` without supported ordinary metric points | No `ResourceMetrics` is emitted for that identity | `target_info`-only input is consumed and outside the round-trip guarantee |
| Renamed `target_info` not recognized by the producer | It is handled as an ordinary metric | Service-metadata round-tripping does not apply |

Other target metadata retains its existing handling but is outside the
round-trip guarantee.

### OTLP to Prometheus

| OTLP input | Prometheus `job` / `instance` | Other labels, metadata, and guarantee |
| :---- | :---- | :---- |
| Neither reserved attribute present | Complete legacy path | Existing configuration behavior, including `keep_identifying_resource_attributes` |
| Complete reserved pair without additional Resource metadata | Reserved pair | No service metadata is synthesized, the pair is not emitted under its translated attribute names, and it alone does not require `target_info` |
| Complete reserved pair with non-empty string `service.*` | Reserved pair | When enabled, generate `target_info` containing exactly the present covered `service.*`, regardless of `keep_identifying_resource_attributes` |
| Complete reserved pair with empty or non-string `service.*` | Reserved pair | Existing translation behavior applies; those service values are outside the guarantee |
| Complete reserved pair with other non-service Resource metadata | Reserved pair | Existing `target_info` handling for other metadata, which remains outside the round-trip guarantee |
| Partial, empty, or non-string reserved pair | Both labels use the legacy identity path, with one bounded diagnostic | Reserved Resource attributes remain filtered; never combine a reserved value with a legacy value |
| Complete reserved pair and conflicting point-level or exporter-added `job` / `instance` | Reserved pair | The conflicting identity is overwritten; other point attributes retain existing handling |
| Point attributes named `prometheus.job` or `prometheus.instance`, with or without a complete Resource pair | Reserved pair when valid; otherwise complete legacy path | Same-named point attributes remain ordinary translated labels and do not activate Option C |
| `target_info` generation disabled | Reserved pair when valid; otherwise complete legacy path | No generated `target_info`; identity remains covered, but service metadata does not |
| `target_info` renamed | Reserved pair when valid; otherwise complete legacy path | Service metadata is covered only if the next producer recognizes the renamed series |
| A present reserved attribute explicitly promoted or converted to an ordinary label | Reserved pair when valid; otherwise complete legacy path | Emit the promoted attribute under its translated name on ordinary metrics, not as `target_info` metadata; the resulting label set and collisions are outside the guarantee |
| Complete reserved pair and `promote_all_resource_attributes` with both reserved attributes ignored | Reserved pair | Default Option C handling for the reserved pair; other Resource attributes retain existing promotion behavior |
| Covered `service.*` uses a name-preserving, collision-free label mapping | Reserved pair | Its presence and value on generated `target_info` are covered |
| Covered `service.*` is renamed or collides after label translation | Reserved pair | The affected service key is outside the guarantee; identity remains covered |

## Rollout Compatibility

The attribute names and behavior must be standardized before consumers assign
the reserved meaning. Consumer support is backward compatible because absence
of a valid pair selects the legacy path. Producer emission is not backward
compatible with consumers that do not recognize the pair, so implementations
MUST add consumer support first and MUST initially gate producer emission behind
an opt-in that defaults to disabled. When disabled, the producer performs the
complete existing Prometheus to OTLP translation and emits no reserved pair.

| Producer | Producer emission | Consumer | Result |
| :---- | :---- | :---- | :---- |
| Existing producer without a reserved pair | Not applicable | Existing consumer | Complete legacy behavior |
| Existing producer without a reserved pair | Not applicable | Option C consumer | Complete legacy behavior because no valid pair is present |
| Option C producer | Disabled | Existing or Option C consumer | Complete legacy producer and consumer behavior |
| Option C producer | Enabled | Option C consumer | Option C contract |
| Option C producer | Enabled | Existing consumer | Unsupported; scrape identity may be lost, replaced, or exposed only as unrelated metadata |

An operator MUST enable producer emission only after every downstream consumer
that synthesizes Prometheus identity, across every fan-out branch, supports
Option C and every intermediary preserves the reserved pair. A consumer needs
no endpoint-specific activation setting: after standardization, the complete
pair is the in-band activation signal. Changing producer emission to default-on
is outside this proposal and requires a separate compatibility decision.

## Round-trip Guarantee and Limits

The round-trip guarantee covers supported ordinary metric points with the
normalized complete scrape identity produced by a conforming Option C producer.
It preserves the exact `job` and `instance` values. For each covered service
attribute, it also preserves its absence or its single non-empty string value
obtained from valid associated `target_info` when generated `target_info` is
enabled and recognized and label-name translation preserves that key without a
collision.

The guarantee does not reproduce the source `target_info` time series or
preserve its one-to-one series or sample presence, timestamps, or cadence; a
consumer may generate new `target_info` only to represent Resource metadata. It
also excludes `target_info`-only input, other target metadata, receiver- or
exporter-added enrichment and external labels, incomplete or malformed scrape
identity, stale, malformed, or conflicting target metadata, empty or non-string
service values, semantics-changing processors, disabled or unrecognized
renamed `target_info`, lossy or colliding label-name translation for the
affected service key, and configurations that explicitly promote either
reserved attribute.
