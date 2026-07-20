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

- `prometheus.job` and `prometheus.instance` are reserved Resource attributes.
  Prometheus MUST apply Option C to all metric points associated with a Resource
  when both attributes are present as non-empty strings.
- Same-named metric data point attributes do not cause Option C to apply and
  remain ordinary metric attributes.
- The reserved attributes are consumed as translation identity. By default,
  they are not emitted under their translated attribute names as additional
  `target_info` metadata labels or ordinary metric labels.

## Prometheus to OTLP

- Store the normalized, complete scrape identity as `prometheus.job` and
  `prometheus.instance` Resource attributes. Do not also store `job` or
  `instance` as metric data point attributes.
- Populate `service.name`, `service.namespace`, and
  `service.instance.id` Resource attributes only from associated
  `target_info`. If they are absent there, leave them absent.
- Do not allow labels on `target_info` to overwrite the reserved scrape
  identity attributes.

## OTLP to Prometheus

- When a valid reserved pair is present, use it atomically as the `job` and
  `instance` labels on ordinary metrics and generated `target_info`. It
  overrides conflicting point-level labels, and neither value is derived from
  `service.*`.
- In this path, include present Resource `service.name`,
  `service.namespace`, and `service.instance.id` attributes on
  `target_info` regardless of `keep_identifying_resource_attributes`.
  Generate `target_info` when these are the only metadata attributes.
- When neither reserved attribute is present, preserve the existing
  service-derived identity and configuration behavior.
- When the pair is partial, empty, or non-string, warn, ignore the override, and
  use the complete legacy translation path. Never combine one reserved value
  with one service-derived value.
- Resource-attribute promotion remains orthogonal. Either reserved attribute
  may be named in `promote_resource_attributes`, and
  `promote_all_resource_attributes` promotes both unless they are excluded by
  `ignore_resource_attributes`; the existing configuration rules still apply.
  Such promotion emits the attributes under their translated names on ordinary
  metric series, not as additional `target_info` metadata. Resulting label sets
  that promote either reserved attribute are outside the round-trip guarantee.

## Translation Scenarios

The tables below summarize the Option C rules above; those rules remain
authoritative. They do not apply to the earlier Summary of Translation Flows.
Here, `service.*` means any individually present subset of `service.name`,
`service.namespace`, and `service.instance.id`, and the legacy path means the
complete existing translation. Translated label names remain subject to the
configured translation strategy. Rows whose conditions occur together are read
together.

### Prometheus to OTLP

| Prometheus input | OTLP Resource attributes | OTLP metric data point attributes and guarantee |
| :---- | :---- | :---- |
| Complete normalized `job` / `instance`, without associated `target_info` | Stored as `prometheus.job` / `prometheus.instance`; covered `service.*` is absent | Scrape `job` / `instance` is not repeated; other ordinary metric labels remain point attributes |
| Complete normalized identity and valid associated `target_info` containing `service.*` | Reserved pair plus exactly the covered `service.*` present on `target_info` | Same point handling as above; `target_info` sample presence, timestamps, and cadence are not represented |
| Ordinary metric labels named `service.*`, `prometheus.job`, or `prometheus.instance` | They do not populate or overwrite Resource identity or service metadata | They remain ordinary point attributes; only associated `target_info` supplies covered service Resource attributes |
| `target_info` labels named `prometheus.job` or `prometheus.instance` | The normalized scrape pair remains authoritative; conflicting metadata cannot overwrite it | Conflicting reserved-name metadata is outside the guarantee; independently valid covered service metadata follows the rule above |
| Stale or conflicting associated `target_info` | The normalized scrape pair remains authoritative | Target metadata is outside the round-trip guarantee |
| Identity still incomplete after normalization | No Option C output is specified | Outside the Option C input domain and round-trip guarantee |
| `target_info` without supported ordinary metric points | No Option C output is specified | Outside the round-trip guarantee |

Other target metadata retains its existing handling but is outside the
round-trip guarantee.

### OTLP to Prometheus

| OTLP input | Prometheus `job` / `instance` | Other labels, metadata, and guarantee |
| :---- | :---- | :---- |
| Neither reserved attribute present | Complete legacy path | Existing configuration behavior, including `keep_identifying_resource_attributes` |
| Complete reserved pair without additional Resource metadata | Reserved pair | No service metadata is synthesized, the pair is not emitted under its translated attribute names, and it alone does not require `target_info` |
| Complete reserved pair with `service.*` | Reserved pair | Generate `target_info` containing exactly the present `service.*`, regardless of `keep_identifying_resource_attributes` |
| Complete reserved pair with other non-service Resource metadata | Reserved pair | Existing `target_info` handling for other metadata, which remains outside the round-trip guarantee |
| Partial, empty, or non-string reserved pair | Complete legacy path, with a warning | Never combine a reserved value with a service-derived value |
| Complete reserved pair and conflicting point-level `job` / `instance` | Reserved pair | The conflicting point identity is overwritten; other point attributes retain existing handling |
| Point attributes named `prometheus.job` or `prometheus.instance`, with or without a complete Resource pair | Reserved pair when valid; otherwise complete legacy path | Same-named point attributes remain ordinary translated labels and do not activate Option C |
| A present reserved attribute promoted by `promote_resource_attributes`, or by `promote_all_resource_attributes` without being ignored | Reserved pair when valid; otherwise complete legacy path | Emit the promoted attribute under its translated name on ordinary metrics, not as `target_info` metadata; the resulting label set and any collision with a point attribute are outside the guarantee |
| Complete reserved pair and `promote_all_resource_attributes` with both reserved attributes ignored | Reserved pair | Default Option C handling for the reserved pair; other Resource attributes retain existing promotion behavior |

## Compatibility and Limits

The complete reserved pair is an in-band opt-in, so native OTLP without it keeps
the current Prometheus behavior and no endpoint configuration is required. The
attribute names must be standardized as reserved before implementations assign
them this meaning.

The round-trip guarantee covers supported ordinary metric points with the
normalized complete scrape identity produced by the Collector Prometheus
receiver. It preserves that identity and, from valid associated `target_info`,
the individual presence and values of `service.name`, `service.namespace`, and
`service.instance.id`.

The guarantee does not preserve the source `target_info` series, including its
sample presence, timestamps, or cadence. It also excludes `target_info`-only
inputs without supported ordinary metric points, other target metadata,
receiver-added enrichment, incomplete or malformed scrape identity, stale or
conflicting target metadata, semantics-changing processors, and configurations
that promote either reserved attribute.

Existing label-name translation still applies to service metadata. Preserving
dotted `service.*` names exactly requires a UTF-8-preserving translation
strategy.
