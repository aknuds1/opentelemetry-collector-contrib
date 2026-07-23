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
reserved pair as authoritative for the `job` and `instance` labels. Resources for which Option C is not active
keep today's service.\*-derived translation unchanged; the Options A/B translation flows above govern only
those Resources.

Deltas versus the Proposed Design above:

- Replaces the **Defaulting service.\* from job/instance** rule and its opt-out toggle: covered attributes are
  never synthesized from scrape identity. When valid target metadata does not supply them, they stay absent.
- The **Aggregated Exporter Fallback** reads the reserved pair instead of bare `job`/`instance` Resource
  attributes; the service.\*-derived fallback for pair-less Resources is unchanged.
- Section 2's OTLP-endpoint `honor_labels` flag remains the compatibility mechanism for **bare**
  `job`/`instance` attributes (Option A). Option C has separate producer-emission and consumer-recognition
  controls, both default-disabled.

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
| Contributor | A Resource or active `target_info` series supplying metadata for one normalized or reserved pair |
| Translation unit | One scrape transaction, one received request or batch, or — for pull exposition — one exposition scrape over the accumulated state |
| Legacy translation | Today's translation behavior, unmodified by Option C |
| Bounded diagnostic | At most one warning or error per affected series or Resource per translation unit, never one per data point |

On an active Option C path, a valid normalized pair is preserved exactly: the producer stores it as reserved
Resource attributes on Prometheus → OTLP, and the consumer emits it verbatim as the `job` and `instance`
labels on OTLP → Prometheus.

Covered attributes are preserved with exact presence and values when supplied by valid associated target
metadata under a matching mapping profile, agreeing across all same-pair contributors. A disagreement, alias
collision, or invalid value omits the affected key with one bounded diagnostic rather than inventing or
misrepresenting a value; the pair remains authoritative wherever it is valid. Operator-selected output
settings (disabled or renamed generation) and declared state loss follow their documented behavior, and the
covered-metadata claim lapses there.

The contract does not preserve the source `target_info` series itself: sample cadence, HELP, UNIT, start
timestamps, and exemplars are not represented. Sample timestamps and stale markers are used only to determine
which target-metadata series are active. Receiver-added enrichment, external labels, explicitly promoted
reserved attributes, and semantics-changing processors are outside the contract.

Activation:

- Producer emission is a configuration opt-in and defaults to disabled.
- Consumer recognition is a separate configuration control and defaults to disabled. Disabled recognition
  performs unchanged legacy translation and treats the reserved attributes as ordinary Resource attributes.
- With recognition enabled, a complete reserved pair activates Option C for that Resource. Only Resource
  attributes activate; same-named data point attributes and metadata labels remain ordinary labels.
- A partial, empty, or non-string pair does not activate. The consumer ignores both reserved values for
  identity, derives `job` and `instance` through legacy translation, handles the invalid reserved attributes
  as ordinary Resource attributes, and reports one bounded diagnostic. One reserved value is never combined
  with one derived value.
- Recognition grants senders no new capability once enabled: any OTLP sender already fully controls `job` and
  `instance` through the covered attributes. The reserved names make that intent explicit and auditable.
- A future default-on change is breaking and requires the applicable major-version compatibility process. The
  recognition control remains available to restore legacy translation during migration.

## Covered Label Mapping

A translator selects the mapping profile before interpreting covered labels:

- Pull paths use the negotiated Prometheus escaping scheme. `allow-utf-8` carries the dotted names directly;
  `dots` and `values` have unambiguous encodings for the three covered names; and `underscores` uses
  `service_name`, `service_namespace`, and `service_instance_id`.
- Remote Write has no escaping negotiation. Its receiver-side profile defaults to `exact`, in which only the
  dotted names are covered. An operator may select `underscores` when the upstream producer uses underscore
  translation. Producer and receiver profiles must match.
- In `underscores` mode, only the three aliases above are reversed. If exact and alias forms both occur with
  the same value, they collapse to one covered attribute. If their values differ, the covered attribute is
  omitted with a bounded diagnostic. Recognized aliases are consumed rather than retained as unrelated
  Resource attributes.
- In `exact` mode, underscore-looking labels remain ordinary metadata.

Prometheus → OTLP decodes the selected profile before merging contributors. OTLP → Prometheus merges raw
Resource attributes before applying the output encoding. Covered output names take precedence: a
non-covered attribute that translates to the same label name is omitted with a bounded diagnostic and never
overwrites or concatenates with the covered value. No profile claims general reversibility for arbitrary
attribute names.

## Prometheus to OTLP

The producer finalizes labels under existing scrape rules (relabeling, `honor_labels` conflict handling, target
filling, and label validation), groups ordinary points by the exact normalized pair, and associates
`target_info`. The pair is stored once per Resource; `job` and `instance` are not repeated as point attributes.

| Scenario | Behavior |
| :---- | :---- |
| Complete pair; no active target metadata | Store the reserved pair; leave covered attributes absent; synthesize no `service.*` |
| Complete pair; valid, agreeing active `target_info` | Store the reserved pair; convert the merged target metadata to Resource attributes; consume the source series |
| Service-looking ordinary label | Keep as an ordinary point attribute; only `target_info` supplies covered attributes |
| `target_info` labels named `prometheus.job` or `prometheus.instance` | Ignore as metadata; they cannot overwrite the reserved pair |
| Identity incomplete after target filling | Fail that series with one bounded diagnostic; emit no partial pair |
| Invalid or conflicting `target_info` | Exclude the invalid series or conflicting key with one bounded diagnostic; valid siblings continue |
| `target_info` whose pair matches no ordinary series in the unit | Consume it without output; a stateful push producer may retain its accepted state for a later request |
| Producer emission disabled | Unchanged legacy translation; no reserved pair emitted |

### Target metadata association

Recognition uses the final relabeled name. A series named exactly `target_info`, with scalar samples and Gauge,
Info, unknown, or no type — for Remote Write 2.0, with Gauge, Info, or unset metadata — is usable target
metadata. Any other type or a histogram shape is invalid target metadata. Suffix-looking names such as
`target_info_total` stay ordinary metrics, and type suffixes are never stripped.

Within one translation unit:

- Identify each source series by its complete final label set. Select its greatest-timestamp sample. Equal
  greatest timestamps are valid only when all selected samples are stale or all are non-stale with value `1`;
  otherwise that series is invalid. A stale selected sample is inactive, and a non-stale value other than `1`
  is invalid.
- Determine all target-metadata state changes before associating ordinary series, so request order cannot
  change the result. Association is a snapshot operation, not a point-by-point temporal join.
- Remove the name, identity labels, and reserved-pair-looking metadata labels; decode the remaining labels
  under the selected mapping profile.
- For a covered key, retain it only if every active contributor supplies the same non-empty string value, or
  every contributor omits it. A presence, type, empty-value, or value disagreement omits that key.
- For other metadata, retain a final Resource key only if every active contributor supplies the same value.
  Presence, value, type, or translated-name disagreement omits that key. Unambiguous keys continue.

Scrape association never crosses translation units. A push producer that carries association across requests
MUST key its state by the exact normalized pair — a hash may index the state but cannot replace exact
equality — scoped per receiver instance and, where applicable, tenant. Within a pair it retains the newest
accepted state per complete `target_info` label set: a newer value-`1` sample replaces the stored metadata, a
newer stale marker retires it, and older samples never resurrect retired metadata. A valid target-info-only
request may commit state. State is bounded; eviction, overflow, or restart invalidates the whole pair entry,
and cross-request preservation applies only while the entry is retained.

If a changed label set is not accompanied by a stale marker for the old series, both remain active. Their
metadata is merged under the agreement rules above; the translator does not silently treat the new series as
a per-key replacement. Remote Write delivery, partial-write accounting, and cross-request atomicity remain
governed by the protocol and receiver.

## OTLP to Prometheus

| Scenario | Behavior |
| :---- | :---- |
| Recognition disabled, or neither reserved attribute present | Unchanged legacy translation, including `keep_identifying_resource_attributes`; the reserved names have no special meaning |
| Valid reserved pair | The pair is authoritative for `job` and `instance` on every ordinary metric and canonical generated `target_info`; neither value is derived from `service.*` |
| One reserved attribute present, or either value empty or non-string | Legacy identity with one bounded diagnostic; never mix reserved and derived values; handle the invalid reserved attributes as ordinary Resource attributes |
| Pair plus conflicting point-level or exporter-added `job`/`instance` | The pair overwrites the conflicting identity; other point attributes keep existing handling |
| Point attributes named `prometheus.job` or `prometheus.instance` | Ordinary translated labels; they never activate Option C |
| Valid covered attributes | Include agreeing covered values on the canonical generated `target_info`, regardless of `keep_identifying_resource_attributes`; on their own they justify generation |
| Multiple Resources with the same pair in one unit | Translate ordinary metrics independently and construct one canonical pair-level `target_info` as described below |
| Reserved attribute explicitly promoted (`promote_resource_attributes`, or `promote_all_resource_attributes` minus `ignore_resource_attributes`) | Emit it under its translated name on ordinary series; identity handling is unchanged |
| `target_info` generation disabled, renamed, or colliding | The setting or existing collision behavior remains authoritative; the pair still supplies identity, but the covered-metadata claim lapses |

### Same-pair canonicalization

Contributors are Resources with the same valid reserved pair and at least one successfully translated ordinary
point in the unit; the single pair-level generated series is called canonical:

- Ordinary metrics retain their original Resource and scope grouping. Only generated target metadata is
  canonicalized at pair level.
- A covered key is included only when every contributor has the same presence and non-empty string value.
  All-absent means absent; any other disagreement omits the key with a bounded diagnostic.
- After reserving the covered Resource keys, intersect other raw Resource attributes by exact key, value, and
  type. Apply existing value conversion and output-name encoding only after that merge. An attribute missing
  from any contributor is omitted; if retained attributes encode to the same label name or a covered output
  name, omit the colliding non-covered labels with a bounded diagnostic.
- Emit at most one generated `target_info` series for the pair in the unit. Its samples follow the consumer's
  existing `target_info` scheduling. If no metadata remains, emit no `target_info`.

This rule satisfies the compatibility requirement of at most one generated target info metric for a unique
`job`/`instance` pair in one output unit, and is new behavior for all three current consumers. It does not
promise one live series across output units or historical metadata changes. Pull scrape staleness and
push-protocol stale markers retire older label sets; without that lifecycle signal, a downstream query may
temporarily see both old and new series.

Output rules:

- The reserved pair is consumed as identity: it is not emitted under translated attribute names as ordinary
  labels or target metadata unless explicitly promoted.
- Generated `target_info` follows existing conventions — a value-`1` `target_info` Gauge, or OpenMetrics
  `target` Info where that representation is preserved — never both.
- Option C does not otherwise change scheduling: ingestion interpolation, Remote Write timestamp selection,
  and timestamp-less pull exposition keep existing behavior.
- Collisions with a real metric named `target_info` follow existing behavior and are outside the
  covered-metadata claim. PromQL matches the concrete `target_info` name, not the OpenMetrics family name
  `target`.

## Non-goals

- No cross-request or cross-output-unit atomicity, batch envelopes, delivery, deduplication, or exactly-once
  semantics.
- No global one-to-one `target_info` guarantee across series lifecycle, missing stale markers, or lost
  association state.
- No protocol response or accounting changes; partial success, Remote Write written counts, and HTTP codes
  retain their existing meaning.
- No preservation of source `target_info` sample timing beyond using timestamps and staleness for association.
- No covered-metadata claim for disabled or renamed target metadata, real metric-name collisions, unsupported
  mapping profiles, promoted label sets, or semantics-changing processors.

## Requirements Mapping

- **Separate Storage**: satisfied by construction — the reserved pair and covered attributes are distinct
  Resource attributes and never overwrite each other.
- **Universal Join Key**: a valid reserved pair supplies `job` and `instance` on every translated ordinary
  metric; otherwise the existing service.\*-derived fallback is unchanged. The requirement guarantees key
  availability, not global uniqueness of a metadata series across its lifecycle.
- **Queryable Resource Attributes**: for Resources carrying a valid reserved pair, agreeing covered attributes
  appear on the canonical `target_info` regardless of `keep_identifying_resource_attributes`; conflicting
  values are omitted rather than misrepresented, and pair-less traffic keeps today's behavior (see Section 2).
- **Non-Breaking Server Compatibility**: both emission and recognition default to disabled. An upgrade without
  a configuration change performs legacy translation even when reserved-looking attributes already exist.

One consequence is deliberate: with producer emission enabled, a target without `target_info` yields a
Resource with **no `service.*` at all**. Generic OTel consumers group such Resources as service-less rather
than under a scrape-config-derived name — per Practical Issue 3, an absent service identity is preferable to
a polluted one. This requires the compatibility specification to repeal, for Option C paths, its current rule
that `service.name` and `service.instance.id` MUST be filled on scrape.

Operators who prefer job-derived service names can still create them deliberately — e.g. an OTTL statement
such as `set(resource.attributes["service.name"], resource.attributes["prometheus.job"])` — turning the
derivation into an explicit per-pipeline choice rather than a default; such a processor is semantics-changing
and intentionally outside the contract.

## Comparison with Options A and B

| Aspect | Option A (bare) | Option B (namespaced) | Option C |
| :---- | :---- | :---- | :---- |
| Resource attributes | `job`, `instance` | `prometheus.job`, `prometheus.instance` | Same as B |
| Consumer activation | Requires the `honor_labels` server flag because bare names already occur without Prometheus provenance | Unspecified | Recognition control, default-disabled; can safely flip default-on at a major version because the reserved names are new and unambiguous, while A's flag must permanently disambiguate bare names |
| `service.*` defaulting from job/instance | Core Rules MAY-default plus toggle | Core Rules MAY-default plus toggle | Never |
| Breaking risk | Several flows marked BREAKING in the tables above | Low | Default configuration is unchanged; misordered opt-in rollout is unsafe |
| Collector / OTTL UX | Natural label names | Prefix must be learned | Prefix must be learned |
| Semantic-convention registration | Arguably none needed | Needed | Needed, as reserved names |

## Rollout

Consumer support ships first. Emission and recognition remain separate controls:

| Consumer input | Consumer recognition | Result |
| :---- | :---- | :---- |
| No complete reserved pair | Disabled or enabled | Unchanged legacy translation |
| Complete reserved pair, whether pre-existing or emitted by Option C | Disabled or unsupported | Legacy interpretation; unsafe for an Option C producer |
| Complete reserved pair, whether pre-existing or emitted by Option C | Enabled | Option C contract; an explicit behavior change for pre-existing pairs |

With emission enabled, an Option C producer emits a complete pair or fails the source series; it never emits a
partial pair.

With recognition disabled or a legacy consumer, an enabled producer fails in one of two ways:

- Without covered attributes, series translate with **no `job` or `instance` labels at all** and the pair is
  silently dropped (absent promotion settings) — legacy consumers suppress `target_info` entirely when no
  identity label is derivable.
- With covered attributes, identity is **silently rewritten** to the service-derived `job`/`instance`, and the
  pair is demoted to escaped `prometheus_job`/`prometheus_instance` labels on `target_info`.

The rollout order is therefore:

1. Deploy consumer support with recognition disabled.
2. Audit for pre-existing complete reserved-looking pairs, then enable recognition on every downstream
   consumer that synthesizes Prometheus identity.
3. Enable producer emission only after step 2 is complete.

Transparent intermediaries need no changes when they preserve Resource attributes. Processors or gateways that
drop, rename, promote, or merge them semantically are outside the contract and must be audited before rollout.
Re-exposing through a pull exporter requires recognition there — the exporter already stamps derived
`job`/`instance` labels on all exposed series, and recognition redirects that stamping to the reserved pair —
plus `honor_labels: true` on the downstream scraper, mirroring federation.

Standardization needs: register `prometheus.job` and `prometheus.instance` as reserved names, amend the
compatibility specification — including the MUST-fill repeal above — and define the semantic controls and
mapping profiles. Consumer recognition is expected to default to enabled in the next major Prometheus
release, alongside Section 2's `honor_labels` and `keep_identifying_resource_attributes` flips — a major
version is where Prometheus may break backwards compatibility. Flipping the producer-emission default is a
separate decision. No wire-version marker is required: activation is explicitly configured, and the
namespaced names carry their own provenance.

## Implementation Notes

Anchors as of current `main` in both repos:

- Collector `prometheusreceiver`: `CreateResource` (`internal/prom_to_otlp.go`) stores the reserved pair
  instead of synthesizing covered attributes; `AddTargetInfo` (`internal/transaction.go`) consumes agreeing
  target metadata under the negotiated mapping profile and already skips `job`/`instance` labels. Identity
  completion already falls back to scrape-target context (`getJobAndInstance`).
- Collector `prometheusremotewritereceiver`: adapt its existing pair-keyed cache (`receiver.go`) to exact pair
  keying and stale-marker retirement per the state rules above.
- Collector `pkg/translator/prometheusremotewrite` (`createAttributes` in `helper.go`, v1 and v2 paths) and
  `prometheusexporter` (`extractJob`/`extractInstance` in `utils.go`): recognition precedes the hard-coded
  service.\* derivation; group same-pair Resources for the canonical target metadata; the pull exporter
  already stamps derived `job`/`instance` on all exposed series (`getMetricMetadata` in `collector.go`).
  Contrib currently lacks Prometheus's `keep_identifying_resource_attributes`/`promote_resource_attributes`
  knobs. The configured translation strategy doubles as the output mapping profile.
- Prometheus OTLP ingestion: the reserved-pair check precedes the service.\* derivation in
  `setResourceContext` (`metrics_to_prw.go`); covered attributes join canonical target metadata and count
  toward the non-identifying-attribute check in `addResourceTargetInfo` (`helper.go`). That translator already
  questions today's behavior — `helper.go`: "XXX: Should we always drop service namespace/service name/service
  instance ID from the labels" — the ambiguity Option C resolves.

Configuration field names are implementation-specific, but producers and consumers expose the applicable
default-disabled emission or recognition control. Remote Write receivers additionally expose a mapping profile
defaulting to `exact`.

## Open Questions

- Venue and process for registering the reserved names (semantic-conventions registry vs. compatibility
  specification only).
- When producer emission flips to default-on. Consumer recognition is expected to flip in the next major
  Prometheus release (see Rollout); confirming that is a Prometheus maintainer decision.
- Whether the contrib Remote Write translator should adopt upstream Prometheus's
  `keep_identifying_resource_attributes` and `promote_resource_attributes` for parity.
- Whether renamed target metadata becomes a standardized, recognizable output rather than remaining outside
  the covered-metadata claim.
- Standardized retention and eviction behavior for push-producer cross-request association state.
- Spec PR 4956 (bare `job`/`instance` Resource attributes) is not accepted by Prometheus maintainers, over the
  assumption that bare names carry Prometheus provenance — the objection Option C's namespacing answers.
  Should a bare-name mapping be revived, a valid reserved pair wins and identity sources are never mixed.
