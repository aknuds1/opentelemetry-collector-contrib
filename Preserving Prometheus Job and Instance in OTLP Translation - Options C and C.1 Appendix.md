# Options C and C.1 — Detailed Design

This is the detailed companion to [Preserving Prometheus Job and Instance in OTLP Translation](<Preserving Prometheus Job and Instance in OTLP Translation.md>). In Google Docs it is the `Options C and C.1 — Detailed Design` tab.

The main document controls Option C's option-level invariants and comparison. This appendix is normative for detailed behavior not repeated there; it may elaborate but not change those invariants. If the two conflict, the main document controls and the appendix must be corrected. Worked examples are explanatory applications of the rules.

# 1. Detailed Normative Contract

## 1.1 Core Contract

Unless overridden here, the existing [Prometheus–OpenMetrics compatibility rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/) and the underlying exposition, OpenMetrics, Remote Write, and OTLP specifications apply.

| Term | Meaning |
| :---- | :---- |
| Producer | A Prometheus or OpenMetrics to OTLP translator that emits Option C attributes |
| Consumer | An OTLP to Prometheus translator that synthesizes `job` and `instance`, such as Prometheus OTLP ingestion or an aggregated Prometheus exporter |
| Reserved pair | `prometheus.job` and `prometheus.instance`, both present as non-empty strings on one Resource |
| Normalized pair | Final `job` and `instance` values after relabeling, `honor_labels` handling, target filling, and validation |
| Covered service attributes | `service.name`, `service.namespace`, and `service.instance.id` |
| Covered service declaration | Any covered service attribute present for entity-less Prometheus translation; a partial declaration remains a declaration |
| Resource identity | Under the Entity data model, the complete set of contained entities plus Resource attributes associated with no entity; Entity descriptions are non-identifying |
| Translation unit | One scrape transaction, one received request or batch, or one pull exposition scrape over accumulated state |
| Legacy translation | Translation behavior in effect before Option C |
| Bounded diagnostic | At most one warning or error per affected series or Resource per translation unit, never one per data point |

On entity-less Resources, a covered service declaration supplies the legacy Prometheus identity labels. The reserved pair is a whole-pair fallback only when every covered service attribute is absent; values from the sources are never mixed. With valid EntityRefs, entity-aware mapping runs first and synthesizes from complete Resource identity. Invalid EntityRefs never activate the fallback.

Option C preserves, per Resource and translation unit:

- The normalized pair exactly, stored as the reserved Resource attributes.
- Covered service attributes obtained from valid associated `target_info`, including exact presence and value when contributors agree.
- One join pair shared by ordinary series and generated `target_info`.

It does not preserve the source `target_info` series itself. HELP, UNIT, exemplars, start timestamps, and its original sample cadence are not represented. Timestamps and stale markers are used only for association state. Receiver enrichment, external labels, explicit Resource-attribute promotion, and semantics-changing processors remain governed by their own contracts.

Producer emission is opt-in and disabled by default. Consumers may independently gate the entity-less fallback. Same-named point attributes remain ordinary labels and never form the Resource pair.

## 1.2 Covered Label Mapping

Covered labels are interpreted in two stages: decode the configured wire encoding, then recognize covered names.

Under Option C:

- On `target_info`, dotted covered names and the three bare underscore forms—`service_name`, `service_namespace`, and `service_instance_id`—are recognized under every profile.
- Equal forms of one covered name collapse. Conflicting values omit that covered attribute and produce one bounded diagnostic.
- Recognized forms are consumed rather than retained as unrelated Resource attributes.
- Recognition is active only when Option C producer emission is enabled. It is a bounded exception to the compatibility specification's default that label keys are not altered.
- The underscore rule is intentionally lossy: the wire cannot distinguish a flattened `service.name` from an attribute literally named `service_name`.

A mapping profile handles reversible encodings:

- `allow-utf-8` carries dotted names directly.
- `dots` encodes `service.name` as `service_dot_name` and a literal `service_name` as `service__name`.
- `values` encodes the dotted name as `U__service_2e_name` while leaving the legacy-valid name unchanged.
- `underscores` has no reversible decoding; Option C recognizes its three covered forms directly.
- Remote Write has no negotiation. Its receiver profile defaults to `exact` and must be configured as `dots` or `values` when that is what the upstream producer used. With the wrong profile, encoded covered names remain ordinary attributes and, under `never-derive`, an otherwise undeclared Resource can fall through to the reserved pair.

Only `target_info` is decoded for covered-attribute recovery. Ordinary-series labels are never decoded, and other underscore-looking labels remain ordinary metadata. Reserved-pair-looking metadata on `target_info` is discarded; the pair comes from the normalized scrape labels.

Prometheus-to-OTLP decoding and recognition happen before contributor merging. OTLP-to-Prometheus output encoding happens after raw Resource attributes are merged. If a covered attribute and a non-covered attribute translate to the same output label, the covered value wins; the other attribute is omitted with one bounded diagnostic rather than concatenated. UTF-8-preserving output cannot produce that collision.

C.1 retains reversible decoding but removes recognition of the three bare underscore forms. Section 3 specifies the resulting differences.

## 1.3 Prometheus to OTLP

The producer finalizes scrape labels, groups ordinary points by the exact normalized pair, and associates `target_info`. It stores the pair once on the Resource and does not repeat `job` or `instance` on every point.

For an entity-less output:

- **Covered service declaration present:** Preserve it. The pair remains Prometheus-side provenance.
- **No covered service declaration:** With `never-derive` enabled, leave `service.*` absent and make the pair available for consumer fallback. With derivation enabled, synthesize covered attributes as today and leave the fallback dormant.

Every unassociated Resource attribute remains identifying in the canonical entity-less OTel Resource model, irrespective of its Prometheus translation role.

| Producer input | Behavior |
| :---- | :---- |
| Complete pair; no target metadata | Store the pair; derive covered attributes under the default, or leave them absent under `never-derive` |
| Complete pair; valid agreeing `target_info` | Store the pair and merge the accepted covered and descriptive attributes |
| Service-looking ordinary label | Keep it as a point attribute; only `target_info` can supply covered Resource attributes |
| Reserved-name label on `target_info` | Drop it so metadata cannot overwrite the scrape-derived pair |
| Pair incomplete after target filling | Fail that series with one bounded diagnostic; emit no partial pair |
| Invalid or conflicting `target_info` | Exclude the invalid series or conflicting key with one bounded diagnostic; valid siblings continue |

### Target metadata association

Classification uses the final relabeled metric name. A scalar series named exactly `target_info` is usable when its type is Gauge, Info, unknown, or absent; Remote Write 2.0 permits Gauge, Info, or unset metadata. Histogram-shaped `target_info` and other types are invalid. Suffix-looking names such as `target_info_total` remain ordinary metrics, and type suffixes are never stripped.

For each translation unit:

1. Fill and validate `job` and `instance` before classification. A `target_info` series without a complete normalized pair is invalid.
2. Group metadata and ordinary series by exact pair equality. Pair values cannot leak across groups.
3. Identify a contributor by its complete final label set and select its greatest-timestamp sample. Equal greatest timestamps are valid only if they are all stale or all non-stale with value `1`.
4. A selected stale sample makes the contributor inactive. A selected non-stale value other than `1` is invalid.
5. Remove the metric name, identity labels, and reserved-pair-looking labels. Decode the remaining metadata under the selected profile.
6. Merge active contributors:
   - Retain a covered key only when every active contributor provides the same non-empty string or every contributor omits it.
   - Retain other metadata only when every contributor provides the same value, presence, type, and translated key.
   - Omit only the disputed key; unambiguous siblings continue.
7. Preserve scalar types exactly. No string/number/bool coercion is performed.
8. With no active contributor, associate no target metadata.

Scrape association does not cross translation units. A push producer that deliberately carries association across requests must:

- Key state by exact normalized-pair equality, scoped by receiver instance and tenant where applicable. A hash may index but cannot replace equality.
- Retain the newest accepted state for each complete `target_info` label set.
- Let a newer value-`1` sample replace metadata and a newer stale marker retire it; older samples never resurrect retired state.
- Permit a valid target-info-only request to update state.
- Bound the state. Eviction, overflow, or restart invalidates the complete pair entry rather than retaining an unproved subset.

If a changed label set arrives without a stale marker for the old series, both contributors remain active and merge under the agreement rules. Remote Write delivery, partial-write accounting, and cross-request atomicity remain governed by their protocols.

## 1.4 OTLP to Prometheus

For an entity-less Resource with a covered service declaration, existing legacy translation remains in force: `job` and `instance` derive from the covered subset and `keep_identifying_resource_attributes` keeps its current meaning. The reserved pair appears as ordinary metadata on generated `target_info`, translated for the output profile, unless explicitly promoted.

| Consumer input | Behavior |
| :---- | :---- |
| Valid EntityRefs | Synthesize the common ordinary-series and `target_info` pair from complete Resource identity |
| Invalid EntityRefs | Apply Entity mapping validation; never fall back |
| Entity-less; covered service declaration present | Use unchanged legacy translation, including partial output |
| Entity-less; no declaration; valid reserved pair | Use the pair verbatim; do not emit the consumed pair again as `target_info` metadata |
| Entity-less; no declaration; incomplete, empty, or non-string pair | Use today's service-less handling, diagnose once, and retain unusable values as ordinary Resource attributes |
| Same-named point attribute | Translate it as an ordinary label; never use it for fallback |
| Reserved Resource attribute explicitly promoted | Emit it under its translated label name without changing identity selection |
| Same-pair fallback fan-in | Generate at most one `target_info` for the pair; retain other Resource attributes only by contributor agreement |
| `target_info` disabled or renamed | Honor the existing setting |

Fan-in among entity-less Resources with covered service declarations remains unchanged. Entity-bearing Resources group by their synthesized pair.

Generated metadata follows existing conventions: one value-`1` `target_info` Gauge, or an OpenMetrics `target` Info where that representation is retained, never both. Scheduling and timestamps continue to follow the consumer. Collisions with a real metric named `target_info` retain existing behavior. Exact round-tripping of arbitrary dotted names still requires UTF-8-preserving output.

## 1.5 Entity Data Model

The Entity data model defines Resource identity as the complete structured set of entities plus Resource attributes associated with no entity. It is not merely the flattened union of `EntityRef.id_keys`: entity types and boundaries matter, while attributes referenced only through `description_keys` are non-identifying.

Option C assumes the planned Prometheus mapping synthesizes `instance` as a UUIDv5 of complete Resource identity and uses the same synthesized pair for ordinary series and `target_info`. The mapping still owns canonical serialization, `job` synthesis, and placement of the original identifying values.

Recommended producer policy:

- **Known application:** When complete and reliable, declare the application entity with covered attributes in `id_keys` and the reserved pair in `description_keys`. A producer may instead emit no EntityRefs and remain on the entity-less legacy path.
- **Undeclared target:** Under `never-derive`, a producer may declare the working-name `prometheus.scrape_target` entity with the pair in `id_keys`. Raw Resource attributes still contribute to complete Resource identity.
- **Relayed input:** Preserve source-authored EntityRefs exactly when a relay format exists. Never repair a malformed set or reinterpret it as absence.
- **Combination:** Do not infer a scrape-target entity beside a relayed or reliably inferred application entity. If the client supplied both, preserve both; both contribute to Resource identity.
- **Pair role:** A pair referenced only by `description_keys` is non-identifying. A pair referenced by `id_keys` identifies that entity. A pair referenced by neither is raw and identifying. Receiver enrichment follows the same rule.

Default-derived `service.*` values do not cause EntityRef emission, preserving today's entity-less behavior until Entity support is explicitly enabled. A deployment may choose scrape-target semantics for a declared target by declaring that entity and making covered attributes descriptive, but output remains synthesized rather than byte-exact.

A valid entity-bearing Resource never uses the verbatim pair fallback. Byte-exact scrape coordinates are an entity-less, undeclared-target property.

# 2. Worked Round Trips

The examples use `foo{A="B"}` for an ordinary metric and omit receiver enrichment unless it changes the point being illustrated. Symbolic Entity-era `job` values and UUIDs stand for rules owned by the planned Prometheus mapping.

## R1 — Declared target, Prometheus to OTLP to Prometheus

An OTel SDK application exposes `target_info{service_name="my_service", service_instance_id="my_instance_id"} 1` and is scraped as `job="my_job"`, `instance="my_instance"`.

- Producer output: `Resource{prometheus.job="my_job", prometheus.instance="my_instance", service.name="my_service", service.instance.id="my_instance_id"}`.
- With `keep_identifying_resource_attributes=false`, the consumer emits `foo{job="my_service", instance="my_instance_id", A="B"}` and `target_info{job="my_service", instance="my_instance_id", prometheus_job="my_job", prometheus_instance="my_instance"} 1`.
- With it enabled, generated `target_info` also carries `service_name="my_service"` and `service_instance_id="my_instance_id"`.
- The covered declaration governs the output pair. Original scrape coordinates remain one metadata join away but do not round-trip as identity labels.

Option C recognizes the flattened covered names, so this result does not depend on whether the exporter or exposition performed flattening.

## R2 — Undeclared target with `never-derive`

A node exporter has no `target_info` and is scraped as `job="node"`, `instance="10.0.0.5:9100"`.

- Producer output: `Resource{prometheus.job="node", prometheus.instance="10.0.0.5:9100"}`.
- The entity-less consumer fallback emits `node_cpu_seconds_total{job="node", instance="10.0.0.5:9100", cpu="0", mode="idle"}`.
- The consumed pair is not duplicated on `target_info`. Receiver enrichment may still generate metadata under the same pair.

This is the byte-exact entity-less fallback case.

## R3 — Undeclared target with default derivation

For the same scrape, the producer emits `Resource{prometheus.job="node", prometheus.instance="10.0.0.5:9100", service.name="node", service.instance.id="10.0.0.5:9100"}`.

Legacy service translation emits the original `job` and `instance`. Generated `target_info` carries the reserved pair as metadata and, when identifying attributes are retained, also the derived `service_name` and `service_instance_id`. Ordinary-series behavior is unchanged from today; pair emission changes the metadata series once at adoption.

## R4 — OTLP-native origin re-scraped through Prometheus

Start with `Resource{service.name="my_service", service.instance.id="my_instance_id", k8s.pod.name="p"}`.

Prometheus emits `foo{job="my_service", instance="my_instance_id"}`. With identifying-attribute retention, `target_info` carries the two service attributes and `k8s_pod_name="p"`; without it, only the latter remains.

A downstream scrape with `honor_labels: true` creates the reserved pair `("my_service", "my_instance_id")`:

- With retained service attributes, Option C restores the covered declaration exactly.
- Without them and with default derivation, it recreates equal `service.*` values from the pair, but declaration versus derivation provenance is lost.
- Without them and with `never-derive`, `service.*` disappears and the prior application identity is reduced to the fallback pair.

Non-covered dotted names such as `k8s.pod.name` remain flattened after the round trip. Full fidelity therefore depends on retaining identifying attributes independently of C's precedence rule.

## R5 — Mixed versions

An old consumer handles R1's Resource through existing service-first translation and treats the reserved pair as ordinary metadata, producing the same output as R1.

An old consumer without fallback support receives R2's Resource as service-less: the ordinary series has no `job` or `instance` and generated `target_info` is suppressed. This is why fallback support must precede `never-derive`.

## R6 — Entity era

For the declared R1 target, the recommended producer adds an application EntityRef such as `{type: service.instance, id_keys: [service.name, service.instance.id], description_keys: [prometheus.job, prometheus.instance]}`. The consumer emits `instance="<UUIDv5 of complete Resource identity>"` for both `foo` and `target_info`. The pair remains descriptive. A native OTLP path converges only when its complete entity set and unassociated raw attributes define the same Resource identity.

For the undeclared R2 target, a producer may add `{type: prometheus.scrape_target, id_keys: [prometheus.job, prometheus.instance]}`. The pair then contributes to complete Resource identity and output is synthesized rather than copied. Original coordinates remain queryable wherever the mapping surfaces identifying attributes.

For R3 under default derivation, the recommended policy emits no EntityRefs. Legacy translation remains byte-identical until `never-derive` and Entity emission are enabled.

An Entity-unaware consumer degrades the application case to R1's flat handling and the scrape-target case to R2's fallback, or to jobless output if it lacks fallback support. Options A or B can retain their pair-first byte-exact property for entity-bearing input only by carving around complete-Resource synthesis.

# 3. Variant C.1 in Detail

## 3.1 Exact Delta from C

C.1 does not reinterpret the bare underscore forms `service_name`, `service_namespace`, and `service_instance_id` as covered attributes after decoding. Reversible `dots` and `values` encodings are still decoded, and dotted names are still covered. Only the ambiguous guess is removed.

A flattened application attribute therefore remains under its literal underscore key. If no dotted covered attribute exists, the Resource is undeclared for entity-less Prometheus mapping: default derivation writes `service.*` from the scrape pair, while `never-derive` leaves `service.*` absent and activates the pair fallback. Targets without `target_info` are unaffected.

This reduces C.1's departure from the rule that label names are not altered, but it also relaxes the requirement that covered application attributes always survive under their semantic names. The same wire spelling can represent either a flattened dotted name or an intentionally underscore-named attribute; C chooses the first interpretation for three bounded names, while C.1 chooses the second.

## 3.2 C.1 Worked Examples

The following variants reuse R1's application and scrape pair.

### V1 — Flattened exposition with default derivation

The producer emits:

`Resource{prometheus.job="my_job", prometheus.instance="my_instance", service.name="my_job", service.instance.id="my_instance", service_name="my_service", service_instance_id="my_instance_id"}`

The underscore attributes are not covered, so today's derivation fills the dotted service attributes from the scrape pair.

- With `keep_identifying_resource_attributes=false`, output identity remains `job="my_job"`, `instance="my_instance"`. Generated `target_info` carries the reserved pair and the raw application-looking underscore attributes.
- With it enabled under escaped output, derived covered attributes and raw underscore attributes translate to the same label names. Covered values win, the raw values are omitted with a bounded diagnostic, and `my_service` can be lost. Today's implementation instead concatenates colliding values; C.1 deliberately replaces that behavior. UTF-8-preserving output avoids the collision.

Ordinary-series labels remain byte-identical to today's scrape, but a generic OTel consumer sees the application identity only under uninterpreted raw keys.

### V2 — Flattened exposition with `never-derive`

The producer emits:

`Resource{prometheus.job="my_job", prometheus.instance="my_instance", service_name="my_service", service_instance_id="my_instance_id"}`

The consumer uses the reserved pair verbatim and emits the raw underscore attributes on `target_info`. This preserves both scrape coordinates and application-looking values, but the latter remain semantically uninterpreted and participate in canonical Resource identity as raw attributes.

### V3 — Dotted or reversibly encoded exposition

If `target_info` carries `service.name` and `service.instance.id`, directly or through a reversible encoding, C.1 is identical to C. R1's covered declaration governs output and the original scrape pair becomes metadata.

The same deployment can therefore move between identity classes solely because an exporter or exposition layer changes how it spells attribute names.

### V4 — Entity era

For flattened `never-derive` input, V2 may carry `{type: prometheus.scrape_target, id_keys: [prometheus.job, prometheus.instance]}`. The raw `service_name` and `service_instance_id` attributes remain unassociated and therefore also contribute to complete Resource identity. A native application declaring `{type: service.instance, id_keys: [service.name, service.instance.id]}` has a different complete identity, so the paths do not converge.

Under default derivation, the recommended producer declares no entities and V1 remains on the entity-less legacy path.

## 3.3 C.1 Tradeoffs

Advantages relative to C:

- No meaning is inferred from a lossy underscore spelling.
- Flattened targets retain scrape `job` and `instance` end to end.
- Producer logic is smaller: reversible decoding remains, but bounded underscore recovery disappears.
- An operator can deliberately rename an underscore key to its dotted semantic name in a processor when that pipeline knows its provenance.

That processor must overwrite any already-derived dotted value. It is semantics-changing, occurs after receiver identity assignment, and cannot restore Entity-era convergence once a scrape-target entity and raw flattened attributes already define identity.

Disadvantages relative to C:

- A visible application declaration is deliberately left unread for flattened exposition.
- Under default derivation, the semantic `service.name` slot still contains scrape configuration.
- With identifying attributes retained and escaped output, application values can be discarded in an output-name collision.
- Flattened scraped and native OTLP paths do not share complete Resource identity.
- Behavior depends on exporter spelling rather than an explicit scrape-side choice.

Against the document requirements, C.1 meets Separate Storage only literally for flattened exposition: both values survive on the Resource, but the application's value is not in the semantic slot. Universal Join Key behaves as under C. Queryable Resource Attributes is not met for flattened exposition because the application value is available only under the underscore key. Its Non-Breaking Server Compatibility caveat is wider because C.1 producers can create the colliding Resource themselves.

The C/C.1 decision is empirical. The useful measurement is the spelling of covered names on scraped `target_info`, not the prevalence of bare `job` or `instance` attributes in OTLP traffic and not the unknowable intent behind a literal `service_name` attribute.

# 4. Compatibility and Rationale

## 4.1 Non-goals

Option C does not provide:

- A setting that makes the reserved pair outrank a covered service declaration on the entity-less path.
- Byte-exact scrape-label round trips for declared or entity-bearing Resources.
- Partitioning for entity-less Resources that project to the same legacy pair. Those Resources merge as they do today; a provenance-only pair does not split them.
- Cross-request or cross-output-unit atomicity, delivery guarantees, deduplication, exactly-once semantics, or protocol-accounting changes.
- Preservation of `target_info` HELP, UNIT, exemplars, original sample timing, or lifecycle beyond its use as association evidence.
- General reversal of arbitrary Prometheus name escaping.

## 4.2 Detailed Requirements Mapping

### Separate Storage

The reserved pair and covered service attributes use distinct Resource keys. Accepted metadata never overwrites the pair, and the pair never overwrites a covered declaration.

Their canonical OTel identity role is separate from their Prometheus role. On an entity-less Resource, every raw attribute—including the pair—is identifying. With EntityRefs, the pair is identifying or descriptive according to its association.

### Universal Join Key

An entity-less covered declaration uses the existing `service.*` mapping. A wholly undeclared Resource can use the complete reserved pair. A valid entity-bearing Resource uses one pair synthesized from complete Resource identity. Ordinary series and generated `target_info` always use the same selected pair.

A partial covered declaration stays partial and is never completed from the reserved pair. Option C preserves existing behavior rather than claiming a complete pair for arbitrary incomplete native OTLP input.

### Queryable Resource Attributes

With `never-derive`, Option C does not manufacture covered service attributes from scrape configuration. Accepted covered attributes are retained as declarations and their Prometheus visibility follows `keep_identifying_resource_attributes`. Values written by an earlier deriving hop are indistinguishable from application-authored values and are relayed as declarations.

C.1 does not meet this requirement for flattened exposition because the value remains under a raw underscore key.

### Non-Breaking Server Compatibility

With producer emission disabled, existing traffic is unchanged. Entity-less consumers retain today's service-first behavior, and the fallback affects only a currently service-less class.

Enabling pair emission is not invisible: raw attributes change entity-less OTel Resource identity, and generated `target_info` gains two labels and therefore a new series identity. A non-default collision also changes where a covered and non-covered Resource attribute translate to the same escaped label: the covered value wins instead of being concatenated.

Under `never-derive`, an undeclared target has no `service.*`. The compatibility specification must therefore repeal its current MUST-fill rule for Option C paths. An operator who wants the old grouping can explicitly derive `service.name` from `prometheus.job` in a processor, accepting that this changes semantics.

## 4.3 Full Option C Benefits

- **Default Prometheus output compatibility:** Existing traffic stays unchanged while emission is disabled, and service-first consumption remains unchanged after pair attributes appear.
- **Covered declarations survive:** Option C recovers the bounded covered names and never lets scrape provenance displace them on the entity-less path.
- **No fabricated service under `never-derive`:** Undeclared targets remain honestly service-less instead of adopting scrape configuration as application identity.
- **Provenance-safe names:** The `prometheus.*` prefix communicates origin and avoids the bare-key ambiguity of A.
- **Minimal entity-less consumer change:** Consumers add a whole-pair fallback after the existing mapping.
- **Entity compatibility:** Entity-bearing Resources use general complete-Resource synthesis without a pair-specific exception.
- **Conditional path convergence:** Scraped and native application telemetry can converge when their complete entity sets and raw identifying attributes match.
- **Operational access:** Original scrape coordinates remain either the fallback identity labels or metadata under the selected join key.

## 4.4 Full Option C Costs

- **Declared targets re-key:** Their Prometheus identity derives from the service declaration rather than the original scrape pair, breaking dashboards, rules, and alerts keyed to scrape labels.
- **Entity-bearing output is synthesized:** Even a scrape-target entity cannot preserve byte-exact source labels without a mapping carve-out.
- **Entity-less raw provenance changes Resource identity:** Adding the reserved pair can split otherwise equal Resources in OTel systems that honor the canonical model.
- **Undeclared targets become service-less:** Generic OTel backends lose the grouping supplied by today's default derivation.
- **Entity-less collisions remain:** Resources with the same covered projection merge before `target_info` can distinguish them.
- **Producer complexity:** Covered-name profiles, contributor association, stale handling, and bounded cross-request state are substantial implementation work.
- **Ambiguous underscore recovery:** C can reinterpret a literal underscore-named attribute as a covered declaration.
- **One-time metadata churn:** Pair labels change generated `target_info` series at adoption.
- **Declaration-status churn:** Adding or removing covered metadata changes which source supplies Prometheus identity.
- **Retention-dependent fidelity:** A Resource transiting Prometheus can lose its covered declaration unless identifying attributes remain on `target_info`.
- **Standardization dependency:** Reserved names, fallback rules, `never-derive`, scrape-target semantics, and the MUST-fill repeal must be standardized.
- **Namespaced UX:** Operators and processor authors must learn the `prometheus.*` prefix.

## 4.5 Expanded Comparison with Option B

Option B stores the same namespaced pair but consults it before the covered service mapping.

Its strongest benefits are:

- Byte-exact scrape `job` and `instance` survive every entity-less target where the consumer honors the pair.
- Namespaced attributes avoid A's bare-name provenance collision.
- Existing derivation can remain untouched.
- It needs less producer-side recovery logic, although push-path `target_info` association remains relevant if metadata is retained.

Its costs are:

- Scrape coordinates become Prometheus label authority and mask an application's covered declaration.
- The same application scraped and pushed directly has path-dependent Prometheus identity.
- Scrape-derived `service.name` pollution remains unless addressed separately.
- Pair-first byte-exact semantics cannot survive complete-Resource UUID synthesis for entity-bearing input without an explicit carve-out.
- Declaring a scrape-target entity is structurally valid, but then scrape identity is one component of total Resource identity and output is synthesized rather than byte-exact.
- Prometheus's own OTLP endpoint does not provide B's primary property until its `honor_labels` behavior changes in a major release.

Thus B's advantage is exact compatibility with source scrape labels, while C's is separation between scrape provenance, legacy service projection, and canonical Entity-aware Resource identity.

# 5. Rollout, Implementation, and Open Work

## 5.1 Rollout

Pair emission, `never-derive`, and EntityRef emission are separate controls.

Legacy entity-less rollout:

1. Deploy consumer support for the whole-pair fallback.
2. Enable producer pair emission, initially leaving current `service.*` derivation on.
3. Enable `never-derive` only after every relevant consumer understands the fallback.
4. Change any producer default later through that implementation's compatibility process.

Turning on `never-derive` can re-key an undeclared target when Entity emission is also enabled: it moves from legacy-derived labels to complete-Resource synthesis. Treat that as an explicit series migration.

Entity rollout:

1. Deploy consumers that validate EntityRefs and synthesize from complete Resource identity.
2. Verify intermediaries preserve Resource attributes and EntityRef structure.
3. Enable producer EntityRef relay or inference.
4. Migrate identity as a new series rather than mutating an existing series silently.

An old consumer can transport pair attributes but does not provide fallback or Entity synthesis. An old consumer seeing an undeclared `never-derive` Resource emits no `job` or `instance`. An Entity-unaware consumer treats referenced attributes as ordinary Resource attributes and follows its flat mapping.

Processors that drop, rename, merge, or promote Resource attributes require audit. Re-exposure through a pull exporter and downstream re-scraping follow existing federation behavior; `honor_labels: true` preserves the labels that exporter emitted.

## 5.2 Standardization Dependencies

The design requires:

- Semantic-convention registration for `prometheus.job` and `prometheus.instance`.
- A final name and identity contract for the proposed scrape-target entity.
- Compatibility-specification rules for pair emission, entity-less fallback, `never-derive`, covered-name recognition, and the MUST-fill repeal.
- Complete-Resource UUIDv5 and `job` synthesis rules for Entity-bearing Resources.
- A defined representation for surfacing identifying and descriptive values under the synthesized join key.
- Feature-gate or equivalent compatibility processes for producer defaults.
- Coordination with the planned `keep_identifying_resource_attributes` and related Prometheus-server changes.

The compatibility specification owns translation behavior; the semantic-conventions registry owns attribute and Entity meanings.

## 5.3 Implementation Anchors

Current implementation anchors include:

- Collector `prometheusreceiver`: `CreateResource` in `internal/prom_to_otlp.go` stores the pair and controls default derivation; `AddTargetInfo` in `internal/transaction.go` performs metadata association; `getJobAndInstance` supplies scrape context.
- Collector `prometheusremotewritereceiver`: its pair-keyed cache in `receiver.go` must use exact equality and the lifecycle rules above.
- Collector `pkg/translator/prometheusremotewrite`: `createAttributes` in `helper.go` retains service-first behavior for entity-less Resources and adds fallback only when every covered attribute and all EntityRefs are absent.
- Collector `prometheusexporter`: `extractJob` and `extractInstance` retain legacy behavior on entity-less input; `getMetricMetadata` stamps the selected pair consistently.
- Prometheus OTLP ingestion: `setResourceContext` retains entity-less service-first mapping, adds whole-pair fallback only for entity-less undeclared input, and delegates Entity-bearing input to complete-Resource synthesis.

Contrib does not currently expose all of Prometheus's `keep_identifying_resource_attributes` and Resource-promotion controls. Producer emission defaults off. Remote Write covered-name decoding defaults to the `exact` profile.

## 5.4 Open Questions

- Process and timing for registering the reserved names and scrape-target entity.
- Whether consumers gate entity-less fallback and whether such a gate ever changes default.
- The final scrape-target entity's minimally sufficient identifying set and identity domain.
- A mechanism for losslessly relaying EntityRef structure through Prometheus exposition.
- Canonical complete-Resource serialization, UUID namespace, and `job` synthesis in the Prometheus Entity mapping.
- Placement of original identifying and descriptive attributes under a synthesized join key.
- Whether contrib Remote Write and pull exporters adopt Prometheus's identifying-attribute retention and promotion controls.
- Whether renamed target metadata becomes a standardized recognizable output.
- Standard retention, eviction, recovery, and diagnostic behavior for cross-request push association.
- Whether the previously rejected bare-name PR 4956 is ever revived. A future bare-name proposal must not mix pair and service sources; namespaced provenance remains the Option C choice unless that decision changes explicitly.
