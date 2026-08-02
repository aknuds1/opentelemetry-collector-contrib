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

# Option C: Namespaced Scrape Provenance and Identity Fallback

Option C stores Prometheus scrape identity on the OTel Resource as **descriptive provenance** — the reserved
attributes `prometheus.job` and `prometheus.instance` - while **respecting each Resource's own identifying
attributes**. A Resource that declares service identity keeps it: the declaration is relayed as identity in
both directions — a change from today's receiver behavior, where the job-derived value can displace the
declaration depending on the exposition's escaping. The pair supplies identity only as a **fallback**, for
targets that declare nothing, replacing today's choice between jobless output and polluting `service.name`
with scrape-config strings. An opt-in never-derive setting stops synthesizing `service.*` from `job` and
`instance`; until opted in, today's derivation is unchanged.

Relative to the Proposed Design above, Option C is the Core Rules with three amendments:

- **Never-derive becomes an opt-in setting**: a producer option stops synthesizing `service.name`,
  `service.namespace`, and `service.instance.id` from `job`/`instance`; the fallback keeps such targets from
  going jobless, and the default flips at a major version alongside Section 2's flips. Until opted in, the
  Core Rules' MAY-default derivation and its toggle are unchanged.
- **Inverted lookup order**: consumers derive identity from the declared `service.*` subset first and fall
  back to the stored pair — the reverse of the Core Rules' pair-first lookup — so the OTel Resource's own
  identity always wins where it exists.
- **Namespaced, descriptive storage**: `prometheus.job`/`prometheus.instance` rather than bare
  `job`/`instance`, carrying provenance in the name (the objection on which bare-name spec PR 4956 was not
  accepted), and stored as metadata rather than authority. Section 2's OTLP-endpoint `honor_labels` flag has
  no role here: nothing ever overrides a declared identity.

## Core Contract

Unless overridden here, the existing [Prometheus–OpenMetrics compatibility rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/)
and the underlying exposition, OpenMetrics, Remote Write, and OTLP specifications apply.

| Term | Meaning |
| :---- | :---- |
| Producer | A Prometheus or OpenMetrics to OTLP translator that emits Option C attributes |
| Consumer | An OTLP to Prometheus translator that synthesizes `job` and `instance` labels, such as Prometheus OTLP ingestion or an aggregated Prometheus exporter |
| Reserved pair | `prometheus.job` and `prometheus.instance`, both present as non-empty strings on one Resource; descriptive provenance, and the identity fallback for undeclared Resources |
| Normalized pair | The final `job` and `instance` label values after relabeling, target filling (filling `job`/`instance` from the scrape-target configuration), and label validation, both non-empty |
| Covered attributes | `service.name`, `service.namespace`, and `service.instance.id` |
| Contributor | An active `target_info` series supplying metadata for one normalized pair during association |
| Declared identity | A Resource's own identifying attributes: entity-declared identifying attributes when entities are present, otherwise the present covered attributes under the default identifying subset. Any present covered attribute constitutes a declared identity; the fallback applies only when none are present |
| Translation unit | One scrape transaction, one received request or batch, or — for pull exposition — one exposition scrape over the accumulated state |
| Legacy translation | Today's translation behavior, unmodified by Option C |
| Bounded diagnostic | At most one warning or error per affected series or Resource per translation unit, never one per data point |

Identity sources rank, highest first: entity-declared identifying attributes; the declared default subset;
the reserved pair, as fallback only. The pair never outranks a declared identity, and values from different
identity sources are never combined.

Option C preserves, per Resource and per translation unit:

- the normalized pair, exactly, as descriptive provenance — stored as the reserved pair on
  Prometheus → OTLP and emitted on generated `target_info` on OTLP → Prometheus — and, for undeclared
  Resources on entity-less paths, verbatim as the output `job` and `instance` labels via the fallback;
- the covered attributes obtained from valid associated `target_info`, with exact presence and values, under
  a matching mapping profile and agreement across same-pair contributors — never dropped in favor of, or
  overwritten by, scrape identity.

It does not preserve the source `target_info` series itself: sample cadence, HELP, UNIT, start timestamps,
and exemplars are not represented. Sample timestamps and stale markers are used only to determine which
target-metadata series are active. Receiver-added enrichment, external labels, explicitly promoted reserved
attributes, and semantics-changing processors are outside the contract.

Producer emission is a configuration opt-in and defaults to disabled (see Rollout). Consumers need no new
behavior for Resources with a declared identity; the fallback is the only consumer addition, it can never
override a declaration, and implementations MAY gate it, although it only changes a case that is degenerate
today. Same-named data point attributes and metadata labels remain ordinary labels and never form a pair.

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

Prometheus → OTLP decodes the selected profile before merging contributors. OTLP → Prometheus applies the
output encoding after merging raw Resource attributes. Covered output names take precedence: a non-covered
attribute that translates to the same label name is omitted with a bounded diagnostic and never overwrites or
concatenates with the covered value. No profile claims general reversibility for arbitrary attribute names.

## Prometheus to OTLP

The producer finalizes labels under existing scrape rules (relabeling, `honor_labels` conflict handling,
target filling, and label validation), groups ordinary points by the exact normalized pair, and associates
`target_info`. The pair is stored once per Resource as the reserved attributes; `job` and `instance` are not
repeated as point attributes. Identity assignment then follows the declaration:

- **Declared target** — valid covered attributes obtained from associated `target_info`: they are the
  Resource's declared identity, exactly as an SDK would have declared them; the pair is descriptive.
- **Undeclared target** — no valid covered attributes: with never-derive opted in, the pair is the
  Resource's identity (see Entity Data Model for the entity-era form) and covered attributes stay absent;
  with derivation on (the default), covered attributes are derived as today and the Resource translates as
  declared-shaped, the fallback dormant.

| Scenario | Behavior |
| :---- | :---- |
| Complete pair; no target metadata | Store the reserved pair; with never-derive opted in, leave covered attributes absent (identity fallback); otherwise derive them as today |
| Complete pair; valid, agreeing active `target_info` | Store the reserved pair descriptively; the merged covered attributes are the declared identity; consume the source series |
| Service-looking ordinary label | Keep as an ordinary point attribute; only `target_info` supplies covered attributes |
| `target_info` labels named `prometheus.job` or `prometheus.instance` | Ignore as metadata; they cannot overwrite the reserved pair |
| Identity incomplete after target filling | Fail that series with one bounded diagnostic; emit no partial pair |
| Invalid or conflicting `target_info` | Exclude the invalid series or conflicting key with one bounded diagnostic; valid siblings continue |
| `target_info` whose pair matches no ordinary series in the unit | Consume it without output; a stateful push producer may retain its accepted state for a later request |
| Producer emission disabled | Unchanged legacy translation; no reserved pair emitted |

### Target metadata association

Classification uses the final relabeled name. A series named exactly `target_info`, with scalar samples and
Gauge, Info, unknown, or no type — for Remote Write 2.0, with Gauge, Info, or unset metadata — is usable
target metadata. Any other type or a histogram shape is invalid target metadata. Suffix-looking names such as
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

Resources with a declared identity translate under **unchanged legacy translation**: `job` and `instance`
derive from the declared subset (or, for entity-bearing payloads, from entity-identity synthesis),
`keep_identifying_resource_attributes` retains its exact meaning, and the reserved pair — an ordinary
descriptive attribute — appears on generated `target_info` under the output mapping profile
(`prometheus_job`, `prometheus_instance`). No new consumer behavior exists for this class.

| Scenario | Behavior |
| :---- | :---- |
| Declared identity present, with or without a reserved pair | Unchanged legacy translation; the pair is ordinary descriptive metadata on generated `target_info` |
| No declared identity; valid reserved pair | Fallback: use the pair verbatim as the `job` and `instance` labels; the consumed pair is not additionally emitted as `target_info` metadata |
| No declared identity; one reserved attribute present, or either value empty or non-string | Today's service-less handling with one bounded diagnostic; never mix reserved and derived values; handle the invalid reserved attributes as ordinary Resource attributes |
| Point attributes named `prometheus.job` or `prometheus.instance` | Ordinary translated labels; the fallback never reads them |
| Reserved attribute explicitly promoted (`promote_resource_attributes`, or `promote_all_resource_attributes` minus `ignore_resource_attributes`) | Emit it under its translated name on ordinary series; identity handling is unchanged |
| Same-pair fan-in among fallback Resources in one unit | Emit at most one generated `target_info` for the pair: covered keys are absent by definition; other attributes merge by agreement, disagreements omitted with a bounded diagnostic; samples follow the consumer's existing `target_info` scheduling |
| `target_info` generation disabled or renamed | The setting remains authoritative |

Fan-in among declared-identity Resources follows existing behavior unchanged — their identity, and therefore
their `target_info` grouping, is exactly what it is today.

Output rules:

- Generated `target_info` follows existing conventions — a value-`1` `target_info` Gauge, or OpenMetrics
  `target` Info where that representation is preserved — never both. Sample scheduling is unchanged:
  ingestion interpolation, Remote Write timestamp selection, and timestamp-less pull exposition keep existing
  behavior.
- Collisions with a real metric named `target_info` follow existing behavior. PromQL matches the concrete
  `target_info` name, not the OpenMetrics family name `target`.
- Exact round-tripping of the dotted covered names requires a UTF-8-preserving translation strategy.

## Entity Data Model

The OpenTelemetry Entity data model (in development) restructures Resource identity: when a payload carries
entities, the identifying resource attribute set is exactly the union of the entities' identifying
attributes — flat attributes are never identifying — and the draft Prometheus entity-ingestion rules
synthesize the `instance` label from that set as a UUIDv5. Option C composes with those rules as the general
case and requests no synthesis carve-outs:

- **Declared targets relay their declared identity**: the recommended producer policy is to declare the
  covered attributes as the `service.instance` entity's identifying attributes — the entity-era encoding of
  the same default-subset convention consumers apply, and the condition under which a scraped application and
  the same application pushing OTLP directly share one synthesized identity. A producer MAY instead declare
  no entities; the consumer's entity-less default then still yields declared-identity semantics via legacy
  label derivation, at the cost of identity convergence with entity-bearing native traffic.
  Exposition-carried entity structure, once a mechanism for relaying it exists, is relayed rather than
  reconstructed.
- **Undeclared targets carry the scrape-target entity**: with never-derive in effect, a scraped target that
  exposes no identifying resource attributes carries the `prometheus.scrape_target` entity (working name)
  whose identifying attributes are the reserved pair — the entity-era form of the fallback, and the sole
  identifying entity on such Resources. Under the default derivation, such targets translate as
  declared-shaped, and the producer declares no entities for them — the entity-less default preserves
  today's translation until never-derive is opted in.
- **The pair's role follows the declaration**: on declared Resources the reserved pair is descriptive and
  rides generated `target_info`; on undeclared Resources it is the identifying set, and its `target_info`
  visibility follows the identifying-attribute partition rather than descriptive handling. Receiver-added
  enrichment stays descriptive, since marking additional entities as identifying changes series identity for
  every consumer under any synthesis.
- Byte-exact `job`/`instance` output labels are therefore an entity-less, undeclared-target property; for
  everything else, identity follows the declaration or the synthesis, and the original scrape coordinates
  remain queryable through the `target_info` join.

In the entity era, identity policy therefore reduces to which entities a producer declares: consumers simply
honor entity-declared identity and need no pair-specific rules. The discipline above — declare the Resource's
own identity where it exists, the scrape-target entity otherwise — is the recommended default. A deployment
that prefers scrape-identity semantics even for declared targets can express that policy by declaring the
scrape-target entity as the identifying entity and keeping the covered attributes descriptive, with no
consumer changes; labels still synthesize from the identifying set, so this buys per-target distinctness and
target-aligned lifecycle, not byte-exact labels.

## Non-goals

- Identity-precedence configuration: no option exists to make the reserved pair outrank a declared identity.
- Byte-exact `job`/`instance` label round-trips for declared-identity Resources or entity-bearing payloads:
  identity follows the declaration or the entity synthesis.
- Partitioning of colliding declarations: Resources that declare the same identity merge exactly as they do
  on the native OTLP path, and the pair does not split them.
- Cross-request or cross-output-unit atomicity, batch envelopes, delivery, deduplication, or exactly-once
  semantics, and protocol response or accounting changes.
- Preservation of source `target_info` sample timing beyond using timestamps and staleness for association:
  staleness and series lifecycle follow existing protocol rules.

## Requirements Mapping

- **Separate Storage**: satisfied by construction — the reserved pair and covered attributes are distinct
  Resource attributes and never overwrite each other; the pair is provenance, the covered attributes are
  identity where declared.
- **Universal Join Key**: declared Resources derive `job`/`instance` exactly as today; undeclared Resources
  gain them through the fallback — strictly better than today, where never-derive alone would leave them
  jobless.
- **Queryable Resource Attributes**: covered attributes are never polluted with scrape-config strings and are
  never dropped in favor of scrape identity; their visibility on `target_info` continues to follow
  `keep_identifying_resource_attributes` and Section 2's planned default flip.
- **Non-Breaking Server Compatibility**: structural — consumer behavior is bit-identical for all existing
  traffic with no configuration change, because declared-identity handling is untouched, the pair is ordinary
  metadata under existing rules, and the fallback activates only for a payload class that is empty today and
  degenerate if it existed. This exceeds the requirement, which only asks that breaks wait for a major
  version; Option C queues none.

One consequence is deliberate: with never-derive in effect, an undeclared target yields a Resource with
**no `service.*` at all**. The fallback supplies its output `job`/`instance`, but generic OTel consumers
group such Resources as service-less rather than under a scrape-config-derived name — per Practical Issue 3,
an absent service identity is preferable to a polluted one. This requires the compatibility specification to
repeal, for Option C paths, its current rule that `service.name` and `service.instance.id` MUST be filled on
scrape.

Operators who prefer job-derived service names can still create them deliberately — e.g. an OTTL statement
such as `set(resource.attributes["service.name"], resource.attributes["prometheus.job"])` — turning the
derivation into an explicit per-pipeline choice rather than a default; such a processor is semantics-changing
and intentionally outside the contract.

## Pros and Cons

Pros:

- **Structural backwards compatibility**: consumer behavior is bit-identical for all existing traffic with no
  configuration change, gates, or major-version flag day. Prometheus's compatibility policy — breaking
  changes only in major versions — is not merely respected but never drawn upon: no break is needed now or
  queued for later. The fallback changes only a payload class that is empty today and degenerate if it
  existed.
- **Declared identity is always respected**: a Resource's own identifying attributes govern translation, so a
  scraped application and the same application pushing OTLP directly share one identity — identity is
  path-independent.
- **The stated pains are solved**: with never-derive in effect, `service.name` is no longer polluted with
  scrape-config strings — per producer today, by default at the major-version flip — neither identity is
  dropped in favor of the other, and undeclared targets gain honest `job`/`instance` join keys instead of
  jobless output or fabricated service names.
- **Provenance-safe names**: `prometheus.job`/`prometheus.instance` state their origin, so a consumer never
  has to guess whether an attribute named `job` means scrape identity, and no `honor_labels`-style
  disambiguation apparatus is needed.
- **Minimal implementation surface**: existing identity derivation is retained unchanged everywhere; each
  consumer adds one fallback conditional, and the entity-era composition requests no synthesis carve-outs.
- **Scrape coordinates stay operable**: the original scrape config and target address are always visible —
  as the identity labels themselves on fallback Resources, and one info-join away on `target_info` for
  declared ones.

Cons:

- **No byte-exact `job`/`instance` round-trip for declared-identity Resources**: an application's series
  re-enter Prometheus under its declared (or entity-synthesized) identity, not the original scrape labels, so
  dashboards and rules keyed on those labels do not survive the OTLP hop. Today's receiver collision behavior
  — the job-derived value displacing the declaration under escaped exposition — is what restores scrape
  labels server-side; Option C replaces that escaping-dependent coin flip with a deterministic rule,
  extending to escaped exposition what already happens under UTF-8.
- **Undeclared targets are service-less on OTel-native backends**: an absent service identity is preferable
  to a polluted one, but with never-derive in effect their grouping regresses relative to defaulting; the
  explicit OTTL derivation is the mitigation.
- **Colliding declarations merge**: Resources declaring the same identity collapse into one series identity,
  inheriting the push path's risk profile; the pair witnesses the collision on `target_info` but does not
  partition it.
- **Identity changes when a target's declaration status changes**: a target that starts (or stops) exposing
  identifying attributes via `target_info` flips between fallback and declared identity, breaking its series
  once — an event triggered by an application change the scrape operator may not control.
- **Standardization is a prerequisite**: reserved-name registration, the fallback semantics, the scrape-target
  entity type, and the MUST-fill repeal must all land before conforming implementations can ship.
- **The namespaced prefix must be learned**: OTTL and processor work targets `prometheus.job`, not `job`.
- **Covered-attribute round-trip fidelity is configuration-dependent**: until Section 2's
  `keep_identifying_resource_attributes` default flip, a declared identity transiting Prometheus and
  re-scraped without its `target_info` metadata is laundered into the pair.

## Comparison with Options A and B

| Aspect | Option A (bare) | Option B (namespaced) | Option C |
| :---- | :---- | :---- | :---- |
| Resource attributes | `job`, `instance` | `prometheus.job`, `prometheus.instance` | Same as B |
| Role of the stored pair | Authoritative identity, looked up first | Unspecified | Descriptive provenance; identity fallback for undeclared Resources only |
| Consumer activation | Requires the `honor_labels` server flag: bare names are generic, unreservable attribute keys a consumer cannot distinguish from scrape identity — whether they already occur in OTLP traffic is unmeasured, but they remain open to collision permanently | Unspecified | None for declared traffic — behavior is unchanged; the fallback MAY be gated |
| `service.*` defaulting from job/instance | Core Rules MAY-default plus toggle | Core Rules MAY-default plus toggle | MAY-default until the major-version flip; opt-in never-derive |
| Breaking risk | Several flows marked BREAKING in the tables above | Low | None structurally; existing traffic translates bit-identically |
| Collector / OTTL UX | Natural label names | Prefix must be learned | Prefix must be learned |
| Semantic-convention registration | Arguably none needed | Needed | Needed, as reserved descriptive names plus fallback semantics |

On the central difference — precedence — pair-first lookup does not eliminate identity overwriting; it
inverts it: observed scrape coordinates displace an application's declared identity, the mirror image of
Practical Issue 1. Declared-first is the only order under which no identity is ever overwritten — every
Resource keeps whichever identity was asserted about it, and the pair fills the gap when none was.

## Rollout

Producer emission is a configuration opt-in and defaults to disabled. Declared-target output translates
without errors on every existing consumer immediately — but its series identity shifts from the original
scrape labels to declaration-derived labels, deliberately and without a knob (see Pros and Cons); today that
shift already occurs for UTF-8-exposition targets, and Option C extends it deterministically to escaped
exposition. Undeclared-target output is unchanged until never-derive is opted in; once it is, consumer
fallback support must deploy first — on a consumer without it, such Resources translate jobless with
`target_info` suppressed, exactly as service-less payloads do today. The order is therefore: deploy consumer
fallback support, then enable emission and never-derive. Flipping never-derive later changes an undeclared
target's entity-era identity once (from legacy-derived labels to the pair's synthesis). Transparent intermediaries need
no changes when they preserve Resource attributes; processors that drop, rename, promote, or merge them
semantically must be audited before rollout. Re-exposure through a pull exporter and re-scraping behave as
federation does today; `honor_labels: true` on the downstream scraper preserves whatever identity labels the
exporter emitted.

Standardization needs: register `prometheus.job` and `prometheus.instance` and the scrape-target entity type
in the semantic-conventions registry (one registration — the registry defines the attributes' meaning and
provenance), and amend the compatibility specification, which references them and defines translation
behavior — including the never-derive setting and, at a major version, its default flip alongside Section 2's
`honor_labels` and `keep_identifying_resource_attributes` flips. The `keep_identifying` flip also closes the
fidelity gap where a declared identity transiting Prometheus is re-scraped without its `target_info`
metadata, and the MUST-fill repeal above applies once never-derive is in effect. No recognition control or
wire marker is required: nothing overrides declared identity, and the namespaced names carry their own
provenance.

## Implementation Notes

Anchors as of current `main` in both repos:

- Collector `prometheusreceiver`: `CreateResource` (`internal/prom_to_otlp.go`) stores the reserved pair and
  stops synthesizing covered attributes from `job`/`instance`; `AddTargetInfo` (`internal/transaction.go`)
  consumes agreeing target metadata under the negotiated mapping profile and already skips `job`/`instance`
  labels. Identity completion already falls back to scrape-target context (`getJobAndInstance` in
  `internal/transaction.go`).
- Collector `prometheusremotewritereceiver`: adapt its existing pair-keyed cache (`receiver.go`) to exact
  pair keying and stale-marker retirement per the state rules above.
- Collector `pkg/translator/prometheusremotewrite` (`createAttributes` in `helper.go`, v1 and v2 paths) and
  `prometheusexporter` (`extractJob`/`extractInstance` in `utils.go`): the existing service.\*-first
  derivation is retained unchanged; add the pair fallback when the declared subset is absent. The pull
  exporter already stamps derived `job`/`instance` on all exposed series (`getMetricMetadata` in
  `collector.go`). Contrib currently lacks Prometheus's
  `keep_identifying_resource_attributes`/`promote_resource_attributes` knobs.
- Prometheus OTLP ingestion: the existing derivation in `setResourceContext` (`metrics_to_prw.go`) is
  retained unchanged; add the pair fallback when `service.name` is absent. The translator's open question —
  `helper.go`: "XXX: Should we always drop service namespace/service name/service instance ID from the
  labels" — is answered by keeping the declared subset authoritative.

Configuration field names are implementation-specific. Producers expose the default-disabled emission
control; Remote Write receivers additionally expose a mapping profile defaulting to `exact`.

## Open Questions

- Process and timing for the semantic-conventions registration of the reserved names and the scrape-target
  entity type (venue resolved: the registry defines the attributes, the compatibility specification defines
  translation behavior).
- Whether consumers gate the fallback, and whether any such gate ever needs a default flip given that the
  fallback cannot override a declaration.
- A mechanism for relaying entity structure through Prometheus exposition (related or referenced entities),
  so a declared target's entity declarations survive the scrape boundary as structure rather than only as
  values.
- Whether the contrib Remote Write translator should adopt upstream Prometheus's
  `keep_identifying_resource_attributes` and `promote_resource_attributes` for parity.
- Whether renamed target metadata becomes a standardized, recognizable output.
- Standardized retention and eviction behavior for push-producer cross-request association state.
- Spec PR 4956 (bare `job`/`instance` Resource attributes) is not accepted by Prometheus maintainers, over
  the assumption that bare names carry Prometheus provenance — the objection Option C's namespacing answers.
  Should a bare-name mapping be revived, the namespaced pair remains descriptive and identity sources are
  never mixed.
