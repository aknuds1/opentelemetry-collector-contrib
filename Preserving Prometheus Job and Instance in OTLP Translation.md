# Preserving Prometheus Job and Instance in OTLP Translation

&nbsp;

# Problem Statement

&nbsp;

Historically, OpenTelemetry specifications have treated Prometheus **job** / **instance** and OpenTelemetry **service.name** / **service.instance.id** as interchangeable representations of the same underlying identity. In practice, they serve fundamentally different purposes:

\- **Prometheus job and instance** identify the scrape configuration and target address.

\- **OpenTelemetry service.name, service.namespace, and service.instance.id** identify the logical application entity.

&nbsp;

## Practical Issues Today

1\. **Data Loss on Scrape**: When scraping a Prometheus endpoint that exposes a target\_info metric containing service.name and service.instance.id, the scraper is currently forced to drop either the Prometheus scrape identity (job/instance) or the OpenTelemetry semantic identity (service.name/service.instance.id).

2\. **Prometheus Backend Expectations**: Users pushing OTLP to Prometheus expect to query for service.name and service.instance.id as standard resource labels, but today must explicitly configure server-side flags (e.g., keep\_identifying\_resource\_attributes=true) to prevent them from being stripped or unconditionally converted into job and instance.

3\. **Pollution of service.name in Kubernetes**: Deriving service.name directly from job often yields non-standard names (e.g., prometheus\_simple/10.42.0.15:8080), breaking correlation with pod logs and OTel SDK traces.

&nbsp;

\---

&nbsp;

# Requirements

&nbsp;

1\. **Separate Storage**: Store job/instance and service.name/service.namespace/service.instance.id separately as OpenTelemetry Resource Attributes when both sets of identifiers exist.

2\. **Universal Join Key**: Always ensure a job/instance pair is available when translating to Prometheus formats (e.g., in aggregated exporters or PRW), even when OTLP is ingested without explicit job/instance resource attributes.

3\. **Queryable Resource Attributes**: Allow users to query for service.name and service.instance.id like any other OTel resource attribute when present.

4\. **Non-Breaking Server Compatibility**: Avoid breaking changes to Prometheus Server default behavior prior to a major version bump.

&nbsp;

\---

&nbsp;

# Proposed Design

&nbsp;

## 1\. Core Rules

\- **Preserve Semantic Identity**: service.name, service.namespace, and service.instance.id from target\_info are preserved as Resource Attributes and are **never dropped**.

\- **Defaulting service.\* from job/instance**: When service.name or service.instance.id are absent on target\_info, receivers **MAY** default them from job and instance.

\- **Opt-Out for Defaulting**: If service.name and service.instance.id are defaulted to job and instance, implementations **MUST** provide a configuration toggle allowing users to disable this behavior.

\- **Aggregated Exporter Fallback (OTLP → Prometheus)**: When exporting metrics from multiple resources, aggregated exporters look up the stored job and instance resource attributes first. If absent, they fall back to synthesizing job from \<service.namespace\>/\<service.name\> (or \<service.name\>) and instance from service.instance.id.

&nbsp;

\---

&nbsp;

## 2\. Prometheus Server Backwards Compatibility (honor\_labels on OTLP Endpoint)

&nbsp;

To avoid breaking existing Prometheus Server OTLP ingestion deployments prior to a major release:

&nbsp;

1\. **OTLP Endpoint honor\_labels Configuration**:

&nbsp;&nbsp;&nbsp;\- Prometheus Server can add an honor\_labels configuration option to its **OTLP endpoint configuration**.

&nbsp;&nbsp;&nbsp;\- When **honor\_labels=true**, the OTLP endpoint respects incoming job and instance resource attributes on the OTLP Resource and uses them directly as the metric's job and instance labels.

&nbsp;&nbsp;&nbsp;\- When **honor\_labels=false** (default for the current major version), the OTLP endpoint preserves existing backwards-compatible behavior by deriving job and instance from service.namespace/service.name and service.instance.id.

2\. **Future Major Version Defaults**:

&nbsp;&nbsp;&nbsp;\- In a future Prometheus Server major release, both honor\_labels (on the OTLP endpoint) and keep\_identifying\_resource\_attributes can switch their default setting to true.

\---

&nbsp;

## 3\. Storing Scrape Identity: Bare (job / instance) vs. Namespaced (prometheus.job / prometheus.instance)

&nbsp;

When preserving the original scrape identity on the OpenTelemetry Resource alongside service.\* attributes, both options ultimately translate back to job and instance labels when exported from OTLP → Prometheus.

&nbsp;

### Option A: Bare (job and instance) (Proposed)

\- **Resource Attributes in OTLP**: job and instance.

\- **Collector / OTTL UX**: Users writing OTTL or Collector processors can naturally inspect and modify job and instance on the OTel Resource without learning a special prefix.

\- **Consistency**: Matches how all other un-namespaced Prometheus labels (container, pod, namespace) are mapped to resource attributes without needing formal semantic convention registration.

&nbsp;

### Option B: Namespaced (prometheus.job and prometheus.instance)

\- **Resource Attributes in OTLP**: prometheus.job and prometheus.instance.

\- **Collector / OTTL UX**: Users modifying metrics in OTel Collector processors would need to know to target prometheus.job instead of job.

&nbsp;

\---

&nbsp;

# Summary of Translation Flows

&nbsp;

&nbsp;

| Direction | Input | Resource Attributes Stored | Output Metric Labels |
| :---- | :---- | :---- | :---- |
| **Prometheus → OTLP** | Scrape job/instance \+ target\_info | job and instance stored alongside service.name / service.instance.id | N/A (OTLP Resource) |
| **OTLP → Prometheus (Aggregated)** | OTLP Resource | Reads stored job/instance (fallback to service.\*) | job and instance labels emitted directly on exported metrics |

&nbsp;

Combinations (Prometheus to OTLP)

&nbsp;To avoid writing so much, let's just look at job and service.name

&nbsp;

| Input series | Input target\_info | Before PR 4956 | After PR 4956 |
| :---- | :---- | :---- | :---- |
| none | none | Error as [service.name](http://service.name) and [service.instance.id](http://service.instance.id) MUST be filled. (prom receiver can guess from target, so there's a chance this works) | Error as job and instance  MUST be added to resource attributes |
| job | none | not explicit, but de-facto become [service.name](http://serice.name) r.a. | explicit, job becomes job r.a. (BREAKING) |
| job | job | same as above | same as above |
| job, service.name | none | not explicit, job becomes [service.name](http://service.name) and r.a.  By the spec, the [service.name](http://service.name) label MUST be r.a. , so this is a conflict, but no resolution. OTel collector prom receiver: source [service.name](http://service.name) becomes [service.name](http://service.name) data point attribute (OTEl coll), violating the spec. | job becomes r.a. (BREAKING).  source [service.name](http://service.name) becomes [service.name](http://service.name) data point attributes, as there's no longer a rule that explicitly makes them r.a. |
| job | job, service.name | Spec says that both job and [service.name](http://service.name) map to [service.name](http://service.name) r.a. No resolution of conflict. OTel collector prom receiver: [service.name](http://service.name) from target\_info prevails over job | no conflict, new job r.a. (BREAKING-ISH as all r.a. are identifying) |
| service.name | none | No special handling, becomes datapoint attribute. OTel collector implements this. | No special handling, becomes datapoint attribute. |
| none | service.name | Spec assumes there's job/instance. OTel collector errors out, no job+instance \- unless from target allocator. | One can read into the spec that job and instance are seeded with empty string. |
| service.name | service.name | Undefined. OTel collector errors out, no job+instance \- unless from target allocator. | Undefined. |

&nbsp;

Combinations (OTLP to Prometheus)

&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;To avoid writing so much, let's just look at job and service.name

&nbsp;

| Input data point attributes | Input resource attributes | Before PR 4956 | After PR 4956 |
| :---- | :---- | :---- | :---- |
| none | service.name | becomes job on metric and target\_info | becomes job on metric and target\_info |
| none | job | not added to metric, remains job in target\_info, no special handling | added to job on metric and target\_info (BREAKING-ISH) |
| job | service.name | not explicit, [service.name](http://service.name) becomes job and overwrites attribute job, on metric and target\_info | not explicit, [service.name](http://service.name) becomes job and overwrites attribute job, on metric and target\_info |
| none | job, service.name | [service.name](http://service.name) becomes job on both metric and target\_info, overwrites job r.a. | job r.a. put on metric and in target\_info, [service.name](http://service.name) only in target\_info (no overwrite, BREAKING) |

&nbsp;

## Appendix \- Claude assessment

## Rules

**Prometheus → OTLP (scrape or Prometheus Remote Write receiver)**

&nbsp;

| Aspect | Before PR | After PR |
| :---- | :---- | :---- |
| **job** scrape label | Consumed; used only to derive **service.name** when **target\_info** didn't override. Not preserved as its own resource attr. | Preserved as resource attribute **job** (MUST). |
| **instance** scrape label | Consumed; used only to derive **service.instance.id** when **target\_info** didn't override. Not preserved. | Preserved as resource attribute **instance** (MUST). |
| **target\_info** labels other than **job**/**instance** | All converted to resource attrs; if **service.name**/**service.instance.id** present, they overwrite the derived values. | Same — copied to resource attrs; **service.name**/**service.instance.id** from **target\_info** win. |
| Default for missing **service.name**/**service.instance.id** on **target\_info** | MUST have **service.name** (=job) and **service.instance.id** (=\<host\>:\<port\> i.e. instance). | MAY default from **job**/**instance**; **implementations MUST provide an opt-out**. |

**OTLP → Prometheus (aggregated / federated / Remote Write exporter)**

&nbsp;

| Aspect | Before PR | After PR |
| :---- | :---- | :---- |
| **job** label | Always derived: **\<service.namespace\>/\<service.name\>** (or just **\<service.name\>**). | If resource attr **job** exists, use it verbatim. Otherwise fall back to the old service.name-based derivation. Empty when neither exists. |
| **instance** label | Always derived from **service.instance.id**; empty otherwise. | If resource attr **instance** exists, use it. Otherwise fall back to **service.instance.id**. Empty otherwise. |
| Resource attrs → metric labels | MAY copy to metric labels if configured, else dropped. | MUST NOT copy by default (essentially unchanged). |
| **target\_info** labels | All resource attrs \+ **job**/**instance**. | All resource attrs \+ **job**/**instance** (unchanged — but note **job**/**instance** may now be resource attrs themselves, so no duplication). |

## Prometheus → OTLP: use cases

Assume the scrape delivers **job=J**, **instance=I** (always present). Column headers **ti.\*** \= labels on **target\_info**. All rows assume defaulting is **enabled** (the default) unless noted.

&nbsp;

| \# | target\_info | ti.service.name | ti.service.instance.id | Other ti.\* | Resource attrs BEFORE | Resource attrs AFTER | Verdict |
| :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- |
| P1 | absent | — | — | — | **service.name=J, service.instance.id=I** | \+ **job=J, instance=I** (else same) | **Additive** — new attrs, no existing values lost. Resource identity changes. |
| P2 | present | absent | absent | none | **service.name=J, service.instance.id=I** | \+ **job=J, instance=I** | **Additive** |
| P3 | present | absent | absent | **k8s.pod=P** | **service.name=J, service.instance.id=I, k8s.pod=P** | \+ **job=J, instance=I** | **Additive** |
| P4 | present | **S** | **X** | **k8s.pod=P** | **service.name=S, service.instance.id=X, k8s.pod=P** | \+ **job=J, instance=I** | **Additive** |
| P5 | present | **S** | absent | — | **service.name=S, service.instance.id=I** (from instance) | \+ **job=J, instance=I** | **Additive** |
| P6 | present | absent | **X** | — | **service.name=J, service.instance.id=X** | \+ **job=J, instance=I** | **Additive** |
| P7 | Relabeled scrape | — | — | — | **service.name/service.instance.id** reflect the relabeled values | Same, plus **job=J', instance=I'** resource attrs (matching relabeled values) | **Additive** |
| P8 | **Defaulting DISABLED** | absent or partial | absent or partial | — | **service.name=J, service.instance.id=I** (old spec MUST) | **service.name** and/or **service.instance.id MISSING** | **BREAKING** — an existing MUST is now optional; consumers relying on **service.name** lose it. |

**Prom → OTLP summary**: With the default configuration, every use case is *additive* (job/instance appear as new resource attributes). Nothing that existed before is removed or altered. The catch: Resource identity changes, so systems that group by full-resource-attribute-set will treat post-PR resources as distinct from pre-PR ones (dual series across the upgrade). Only P8 (opt-out flag) is a hard breaking change to existing consumer queries.

## OTLP → Prometheus: use cases

Inputs are OTLP resource attributes. **Ns, S, Sid** \= **service.namespace, service.name, service.instance.id**. **Jra, Ira** \= **job** and **instance** resource attributes.

&nbsp;

| \# | S | Ns | Sid | Jra | Ira | Prom job BEFORE | Prom instance BEFORE | Prom job AFTER | Prom instance AFTER | Verdict |
| :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- | :---- |
| O1 | **svc** | — | **sid** | — | — | **svc** | **sid** | **svc** | **sid** | **No change** |
| O2 | **svc** | **ns** | **sid** | — | — | **ns/svc** | **sid** | **ns/svc** | **sid** | **No change** |
| O3 | **svc** | — | — | — | — | **svc** | **""** (empty) | **svc** | **""** | **No change** |
| O4 | — | — | — | — | — | **""** (ambiguous in old spec) | **""** | **""** | **""** | **No change** (old ambiguity resolved) |
| O5 | **svc** | — | **sid** | **J** | **I** | **svc**; **Jra/Ira** would only surface in **target\_info** labels | **sid** | **J** | **I** | **BREAKING** — **job/instance** label values differ from before. |
| O6 | **svc** | — | **sid** | — | **I** | **svc** | **sid** | **svc** | **I** | **BREAKING** — **instance** value changes. |
| O7 | **svc** | — | **sid** | **J** | — | **svc** | **sid** | **J** | **sid** | **BREAKING** — **job** value changes. |
| O8 | — | — | — | **J** | **I** | **"" / ""** | **"" / ""** | **J** | **I** | **BREAKING** (in a good way) — previously produced empty identity, now round-trips the scraped identity. |
| O9 | **svc** | — | **sid**, resource also carries **k8s.pod=P** etc. | — | — | **job=svc, instance=sid; target\_info** has **service.name=svc, service.instance.id=sid, k8s.pod=P** | same | same | same | **No change** for metric labels. **target\_info** unchanged. |
| O10 | Any Prom→OTLP→Prom round-trip (P1–P7 output feeds O5–O8) |  |  | present | present | see O5–O8 |  |  |  | **BREAKING** in existing pipelines. This is the deliberate behavior change the PR enables — identity is preserved end-to-end. |

**OTLP → Prom summary**: If your OTLP data never carries **job/instance** as resource attributes (the normal, pre-PR world), nothing changes. The behavior change only kicks in when those attributes are present — which is precisely the new capability introduced by the receiver side. So in a mixed rollout, a downstream aggregating exporter that consumes post-PR receiver output will emit different **job/instance** label values than the same data emitted from a pre-PR receiver (O5–O8, O10).

## Bottom line

* **Prom → OTLP direction (default config)**: **Additive breaking** — no attribute values change, but every Resource gains **job/instance**, shifting Resource identity. Consumers keying on the exact resource-attribute set see new streams across the upgrade boundary.  
* **Prom → OTLP direction (opt-out flag enabled)**: **Hard breaking** — **service.name/service.instance.id** can now be absent; the previous spec required them.  
* **OTLP → Prom direction alone**: **No change** for pipelines that never had **job/instance** as OTLP resource attributes.  
* **End-to-end (Prom → OTLP → Prom) after full rollout**: **Behavior change** — **job/instance** now round-trip verbatim rather than being reconstructed from **service.name/service.instance.id**. This is the intended fix.

There are also two subtle spec tightenings I noticed while reading: the PR explicitly excludes **job/instance** labels of **target\_info** from resource-attribute conversion (previously the "all labels" wording was ambiguous), and it explicitly handles the empty **service.name/service.instance.id** case with empty label values instead of leaving it undefined. Both close ambiguities rather than introduce new observable behavior.

&nbsp;

&nbsp;

# Use case

&nbsp;

* let's write down some use cases and what's wrong with them (that problem statement is operator view, not end-user view I'm afraid)

&nbsp;

Opentelemetry SDK W/ prom exporter

* target\_info {service.name \+ service.instance.id} 1

Prometheus Scrapes it

* Get target\_info w/ service.name \+ service.instance.id \+ job \+ instance  
* service.name \+ service.instance.id (usually) differ from job \+ instance

&nbsp;

OTLP \-\> Prom

job/instance

&nbsp;

**Before PR**

Opentelemetry SDK W/ prom exporter

* target\_info {service.name=my\_service, service.instance.id=my\_instance\_id} 1

Collector Prometheus receiver

* UTF-8 no transform: Resource{service.name=my\_job, service.instance.id=my\_instance}  **\<- data loss problem**  
* Underscore escaping: Resource{service\_name=my\_service,service.name=my\_job,service\_instance\_id=my\_instance\_id,service.instance.id=my\_instance} **\<- this just looks weird**

\[OTTL processors in the pipeline, optional\]

OTLP Export \-\> Prometheus

* UTF-8 no transform: target\_info {job=my\_job, instance=my\_instance} **\<- data still lost, but at least job/instance are normal.**  
* Underscore escaping: target\_info {job=my\_job, instance=my\_instance, service\_name=my\_service, service\_isntance\_id=my\_instance} **\<- this isn't as bad as it was in the collector.**

&nbsp;

**After PR End State**

&nbsp;

Opentelemetry SDK W/ prom exporter

* target\_info {service.name=my\_service, service.instance.id=my\_instance\_id} 1

Collector Prometheus receiver

* UTF-8 no transform: Resource{job=my\_job, instance=my\_instance, service.name=my\_service, service.instance.id=my\_instance\_id}  **\<- no data loss**  
* Underscore escaping: Resource{job=my\_job, instance=my\_instance, service\_name=my\_service, service\_instance\_id=my\_instance\_id} **\<- looks more normal**

\[OTTL processors in the pipeline, optional\]

OTLP Export \-\> Prometheus

* UTF-8 no transform: target\_info {job=my\_job, instance=my\_instance, service.name=my\_service, service.instance.id=my\_instance\_id} **\<- New service.name/service.instance.id labels.**  
* Underscore escaping: target\_info {job=my\_job, instance=my\_instance, service\_name=my\_service, service\_instance\_id=my\_instance} **\<-  No change from before.**

&nbsp;

**After PR (Old Collector, new server)**

&nbsp;

Opentelemetry SDK W/ prom exporter

* target\_info {service.name=my\_service, service.instance.id=my\_instance\_id} 1

Collector Prometheus receiver

* UTF-8 no transform: Resource{service.name=my\_job, service.instance.id=my\_instance}  
* Underscore escaping: Resource{service\_name=my\_service,service.name=my\_job,service\_instance\_id=my\_instance\_id,service.instance.id=my\_instance}

\[OTTL processors in the pipeline, optional\]

OTLP Export \-\> Prometheus

* UTF-8 no transform: target\_info {job=my\_job, instance=my\_instance} **\<- Same as original behavior**  
* Underscore escaping: target\_info {job=my\_job, instance=my\_instance, service\_name=my\_service, service\_isntance\_id=my\_instance} **\<-  Same as original behavior**

&nbsp;

**After PR (New Collector, current Prom server behavior)**

&nbsp;

Opentelemetry SDK W/ prom exporter

* target\_info {service.name=my\_service, service.instance.id=my\_instance\_id} 1

Collector Prometheus receiver

* UTF-8 no transform: Resource{job=my\_job, instance=my\_instance, service.name=my\_service, service.instance.id=my\_instance\_id}  
* Underscore escaping: Resource{job=my\_job, instance=my\_instance, service\_name=my\_service, service\_instance\_id=my\_instance\_id}

\[OTTL processors in the pipeline, optional\]

OTLP Export \-\> Prometheus

* UTF-8 no transform: target\_info {job=my\_service, instance=my\_instance\_id}  **\<- This is not a great outcome, as job/instance changed\!**  
* Underscore escaping: target\_info {service\_name=my\_service, service\_instance\_id=my\_instance} **\<- No Job/Instance at all \!?\!?\!?\!**

&nbsp;

**Issues:**

* **With UTF-8 Enabled, service.name and service.instance.id are dropped**  
* **Users that want to use OTTL on job/instance in the collector need to target service.name/service.instance.id.**

&nbsp;

OTLP \-\> Prom \-\> OTLP \-\> Prom

* let's take a look at the cases I collected and then the ones by claude \- do they make sense?  
* figure out if honor\_labels actually work or we should say "legacy\_behavior"  
* decide on the namespace question , as in use naked "job" r.a. or "prometheus.job" ?

&nbsp;

&nbsp;

User "How do change my job label in ottl?" Actually change [service.name](http://service.name).

&nbsp;

&nbsp;

### Example

&nbsp;

Resource{job=my\_job, instance=my\_instance, service.name=my\_service, [service.instance.id](http://service.instance.id)\=my\_instance\_id}

* Metric{name=foo, labels{A=B}} 1

&nbsp;

Becomes (today) keep\_identifying\_attributes \= false \- by spec (as spec doesn't specify it), implemented by Prom

* target\_info {job=my\_service, instance=my\_instance\_id}  
* foo {job=my\_service, instance=my\_instance\_id, A=B} 1

&nbsp;

Becomes (today) keep\_identifying\_attributes \= true \- implemented by Prom

* target\_info {job=my\_service, instance=my\_instance\_id, [service.name](http://survive.name)\=my\_service, service.instance.id=my\_instance\_id}  
* foo {job=my\_service, instance=my\_instance\_id, A=B} 1

&nbsp;

Becomes (new) \- by spec

* target\_info {job=my\_job, instance=my\_instance, service.name=my\_service, service.instance.id=my\_instance\_id}  
* foo {**job=my\_job**, **instance=my\_instance**, A=B} 1

&nbsp;

Becomes (future prometheus)

* Have to enable "honor\_labels" \= true  (we'd want this to be default in 4.0)  
* target\_info {job=my\_job, instance=my\_instance, service.name=my\_service, service.instance.id=my\_instance\_id}  
* foo {**job=my\_job**, **instance=my\_instance**, A=B} 1

&nbsp;

Changing what populates job and instance doesn't just change target\_info.  It changes job/instance FOR ALL METRICS.

&nbsp;

&nbsp;

Example:

&nbsp;

Application service my\_service\_name\_1  using OTel SDK Prometheus exporter  \<-\> scrape job 1 instance 1

Application service my\_service\_name\_2  using OTel SDK Prometheus exporter  \<-\> scrape job 2 instance 2

&nbsp;

Application service my\_service\_name\_1  using OTel SDK Prometheus exporter  \<-\> scrape job 1 instance 1

Application service my\_service\_name\_2  using OTel SDK Prometheus exporter  \<-\> scrape job 1 instance 2

&nbsp;

If 2 application services export on one endpoint they should generate at least instance label that Prometheus can honor (honor\_labels).

# Option C: Namespaced Scrape Provenance and Identity Fallback

Option C stores normalized Prometheus scrape coordinates on the OTel Resource as `prometheus.job` and `prometheus.instance` while keeping a covered service declaration authoritative for entity-less Prometheus translation. The reserved pair is used verbatim only when that declaration is absent. An opt-in `never-derive` setting stops producers from filling `service.*` from scrape coordinates; the current derivation remains the default until changed through the producer's compatibility process.

The exact mapping, association state, worked examples, and implementation notes are in the [Options C and C.1 — Detailed Design appendix](<Preserving Prometheus Job and Instance in OTLP Translation - Options C and C.1 Appendix.md>) (the `Options C and C.1 — Detailed Design` tab in Google Docs).

## Essential Terms

| Term | Meaning |
| :---- | :---- |
| Reserved pair | `prometheus.job` and `prometheus.instance`, both present as non-empty strings on one Resource |
| Covered service attributes | `service.name`, `service.namespace`, and `service.instance.id` |
| Resource identity | Under the Entity data model, the complete set of contained entities plus Resource attributes associated with no entity; entity descriptions are non-identifying |

“Covered service declaration” below means any covered service attribute used by the entity-less legacy mapping. It is not a synonym for complete OTel Resource identity.

## Design

Relative to the Proposed Design, Option C makes three choices:

- **Namespaced storage:** Store the normalized scrape pair under Prometheus-specific names, once per Resource, rather than as bare `job` and `instance` attributes or repeated point attributes.
- **Declared-first entity-less mapping:** Preserve any covered service declaration and use the reserved pair only when all covered service attributes are absent. Values from the two sources are never combined.
- **Opt-in never-derive:** Allow producers to stop synthesizing covered service attributes from `job` and `instance`. Until enabled, today's derivation remains unchanged and normally keeps the fallback dormant.

Because the reserved pair never outranks a covered service declaration, Section 2's OTLP-endpoint `honor_labels` flag has no role in Option C.

Prometheus-to-OTLP producers obtain the pair after relabeling, `honor_labels` handling, target filling, and validation. They associate `target_info` by the exact pair. Covered service attributes are accepted only from valid, active contributors that agree; ordinary metric labels never become Resource declarations merely because they use a covered or reserved name. Option C recognizes the three covered underscore spellings on `target_info` as a bounded compatibility rule. C.1 removes that recognition.

OTLP-to-Prometheus consumers follow this order:

| Input | Prometheus identity behavior |
| :---- | :---- |
| Valid EntityRefs present | Run entity-aware translation first and synthesize from complete Resource identity; never use the verbatim fallback |
| Invalid EntityRefs present | Follow Entity mapping validation; never reinterpret malformed EntityRefs as absence or activate the fallback |
| No EntityRefs; any covered service attribute present | Use unchanged legacy `service.*` translation. A partial declaration remains partial; never fill its missing label from the reserved pair |
| No EntityRefs; no covered attributes; valid reserved pair | Use the pair verbatim as `job` and `instance`. The consumed pair is not also emitted as `target_info` metadata |
| No EntityRefs; no covered attributes; invalid or incomplete pair | Use today's service-less handling, emit one bounded diagnostic, and treat the unusable reserved attributes as ordinary Resource attributes |
| Same-named point attributes | Translate them as ordinary labels; they never participate in Resource fallback |

On a declared entity-less Resource, the reserved pair remains ordinary `target_info` metadata unless explicitly promoted. Same-pair fan-in, output-name collisions, `target_info` scheduling, and the precise validation rules are specified in the detailed appendix.

## Entity Data Model Compatibility

The Entity data model and concrete Prometheus Entity mapping are still in development. Option C assumes that the planned mapping synthesizes `instance` as a UUIDv5 of complete Resource identity. Entity type and boundaries remain significant, unassociated raw Resource attributes participate in identity, and attributes referenced only through `description_keys` do not.

The recommended producer policy is:

- For a reliably known application entity, reference covered service attributes through its `id_keys` and the reserved pair through `description_keys`. A producer may instead remain entity-less.
- For an otherwise undeclared target under `never-derive`, a producer may emit the proposed `prometheus.scrape_target` entity with the reserved pair in `id_keys`. This working-name entity is optional, not implied merely by carrying the pair.
- Relay source-authored EntityRefs exactly when a relay mechanism exists. Do not add a scrape-target entity beside a relayed or reliably inferred application entity, although both entities contribute when the client supplied both.
- If the pair is referenced only by `description_keys`, it is non-identifying; if referenced by `id_keys`, it identifies that entity; if referenced by neither, it is raw and identifying.

Every valid entity-bearing Resource uses complete-Resource synthesis. Byte-exact `job` and `instance` output is therefore limited to the entity-less fallback. The mapping still needs to define canonical UUID input encoding, `job` synthesis, and where original identifying values surface.

## Requirements Mapping

| Requirement | Option C |
| :---- | :---- |
| Separate Storage | The scrape pair and covered service attributes occupy distinct Resource keys and never overwrite each other |
| Universal Join Key | Entity-less declared Resources use the legacy mapping, undeclared Resources with a valid pair gain one through fallback, and entity-bearing Resources use one synthesized pair for ordinary series and `target_info` |
| Queryable Resource Attributes | Under `never-derive`, scrape configuration is not written into covered service attributes; preservation through Prometheus still depends on `keep_identifying_resource_attributes` |
| Non-Breaking Server Compatibility | Defaults remain unchanged for existing traffic. Pair emission can reshape generated `target_info` and changes canonical entity-less Resource identity; one non-default escaped-name collision also changes as detailed in the appendix |

## Pros and Cons

Benefits:

- Existing entity-less service-first translation remains unchanged, and the fallback cannot override a covered declaration.
- Scrape coordinates remain queryable: directly as fallback labels or as metadata joined through the synthesized or service-derived pair.
- The Entity path uses the general complete-Resource synthesis rule without a pair-specific carve-out.
- `never-derive` avoids manufacturing service identity from monitoring configuration.

Costs:

- Declared and entity-bearing Resources do not round-trip the original scrape `job` and `instance` byte-for-byte.
- Entity-less pair attributes are raw and therefore change canonical OTel Resource identity even when Prometheus treats them as provenance.
- Under `never-derive`, an undeclared target is service-less to generic OTel consumers.
- Option C's underscore recognition intentionally guesses that three ambiguous `target_info` labels are flattened covered names.
- Targets can re-key when their declaration status changes, and generated `target_info` changes once when pair metadata appears.
- Producer association machinery, semantic-convention registration, the `prometheus.scrape_target` type, and a compatibility-specification change are prerequisites.

The detailed appendix retains the complete benefits, caveats, non-goals, collision cases, and compatibility analysis.

## Comparison with Options A and B

| Aspect | Option A | Option B | Option C |
| :---- | :---- | :---- | :---- |
| Stored attributes | Bare `job` and `instance` | Namespaced pair | Namespaced pair |
| Entity-less precedence | Pair first | Pair first | Covered service declaration first; pair only as fallback |
| Undeclared target | Pair first | Pair first | Verbatim fallback, especially with `never-derive` |
| Declared-target round trip | Preserves scrape coordinates where the consumer honors the pair | Preserves scrape coordinates where the consumer honors the pair | Preserves the covered declaration, not scrape coordinates |
| `service.*` defaulting | Existing MAY-default plus toggle | Existing MAY-default plus toggle | Existing default plus opt-in `never-derive` |
| Entity-bearing input | Exact pair-first output needs a carve-out from complete-Resource synthesis | Structurally representable, but byte-exact output still needs a carve-out | Complete-Resource synthesis with no carve-out |
| Principal risk | Bare-name collision and ambiguous provenance | Scrape identity masks the application declaration | Declared targets re-key; underscore recovery is ambiguous |

The independent choices are therefore naming, entity-less precedence, and covered-name recovery. B, C, and C.1 agree on namespaced storage; B chooses pair-first projection, C and C.1 choose service-first projection, and C.1 declines C's lossy underscore recognition. Canonical entity-aware Resource identity is compositional under every option.

## Variant C.1: Without Covered-Name Recognition

C.1 removes one Option C rule: after reversible decoding, a bare `service_name`, `service_namespace`, or `service_instance_id` on `target_info` is not reinterpreted as the corresponding dotted covered attribute. It remains an ordinary Resource attribute. Dotted names and names recovered unambiguously through `dots` or `values` encoding behave exactly as in C.

| Input spelling and mode | C.1 outcome |
| :---- | :---- |
| Dotted or reversibly encoded covered name | Same covered service declaration and output as C |
| Bare underscore form; default derivation | Treat the target as undeclared, derive `service.*` from the scrape pair as today, and retain the application-looking value as a raw attribute |
| Bare underscore form; `never-derive` | Leave `service.*` absent and use the reserved pair as the entity-less fallback; the raw flattened attributes remain identifying |
| Entity-bearing flattened input | Include unassociated flattened attributes in complete Resource identity; it does not converge with a native application entity unless the complete identities match |

C.1 avoids guessing what an ambiguous underscore name means and narrows the declared-target compatibility change to dotted or reversibly encoded exposition. In exchange, it deliberately leaves a visible flattened application declaration uninterpreted, can lose that value through an escaped-output collision when identifying attributes are retained, fails Queryable Resource Attributes for that class, and makes behavior depend on exporter spelling. C versus C.1 is therefore an empirical choice about how often covered names reach producers flattened.

## Rollout

The legacy and Entity paths have independent ordering:

1. Deploy entity-less consumer fallback support.
2. Enable producer pair emission.
3. Enable `never-derive` only after all relevant consumers understand the fallback.
4. Separately, deploy Entity-aware consumers and EntityRef-preserving intermediaries before enabling producer EntityRef emission.

Pair emission is opt-in and disabled by default. Entity-less declared output remains consumable by existing consumers, while an old consumer receiving an undeclared `never-derive` Resource produces no `job` or `instance`. An Entity-unaware consumer follows flat behavior: application attributes use legacy service mapping, while an otherwise undeclared scrape-target Resource uses the pair only if that consumer supports the fallback.

## Open Questions

- Registration and final semantics of `prometheus.job`, `prometheus.instance`, and the proposed scrape-target entity.
- Whether consumers gate the entity-less fallback.
- How EntityRef structure is relayed through Prometheus exposition.

Detailed implementation questions—promotion parity, renamed metadata, state retention and eviction, and the final Entity mapping—remain in the appendix.

# Option D: Do Nothing

Change no specification and ship no new attribute. Today's behavior stands: the receiver fills `service.name` from `job` and `service.instance.id` from `instance` for every scraped target, then merges `target_info` labels over that Resource, so dotted covered names overwrite the derived values while flattened ones land as stray attributes of their own — and the scrape pair itself is consumed rather than stored. On the way back, `job` comes from `service.namespace` plus `service.name` and `instance` from `service.instance.id`, with the covered attributes stripped from generated `target_info` unless `keep_identifying_resource_attributes` is enabled. Option D's outcomes are therefore the Today column of Who changes, by input path.

&nbsp;

Pros:

* **No migration and no new surface**: No label changes, so no dashboards, rules, or alerts move; no new configuration, gate, or rollout ordering; no reserved names to register and no compatibility-specification amendment.  
* **No implementation cost**: Nothing to build or maintain in Prometheus, the Collector, or the SDKs, and none of the association or proof machinery in the appendix.  
* **Nothing is foreclosed before entity identity settles**: The Entity data model is at Development maturity and will define Resource identity; fixing a precedence rule now risks standardizing the wrong one, or one the entity mapping later has to carve around.  
* **Motivated deployments already have escape hatches**: `keep_identifying_resource_attributes` retains the covered attributes, UTF-8-preserving exposition preserves dotted declarations, and OTTL can rewrite either direction per pipeline.

&nbsp;

Cons:

* **Practical Issue 1 stands, and irreversibly**: The scrape pair is consumed, so no round trip can restore the original `job`/`instance` for any target. Provenance is lost rather than re-keyed, which is the one outcome a later design cannot repair retroactively.  
* **Practical Issue 3 stands, by default**: **service.name** is filled from the scrape configuration for every target that does not expose dotted covered names — the populous path — so scrape-configuration strings keep occupying the semantic slot and keep breaking correlation with pod logs and SDK traces. Because it is filled for every scraped target, its presence also cannot tell a consumer whether a Resource represents a service.  
* **The outcome follows exposition accident rather than configuration**: Whether an application's declaration survives depends on whether its exporter flattened the names and what escaping the exposition negotiated, and no scrape-side setting corrects it. Of the options here this is the least predictable.  
* **Doing nothing does not hold the line, it delegates**: With no agreed rule, a receiver that makes `job`/`instance` round-trip by default reaches Option B's effect without any Prometheus server change and without review — see Open Questions. The decision then belongs to whichever implementation ships first.  
* **The entity question arrives unanswered**: Identity is not avoided, only deferred, so whatever the entity mapping assumes about scraped Resources becomes the answer by default.  
* **Spec PR 4956 stays unresolved**, as does the collector gap behind the service-catalog and metering complaints — though that gap is contrib parity work and independent of which option wins.

&nbsp;

Against the document's requirements: Separate Storage is **not met**, the scrape pair being consumed; Universal Join Key is met only where the covered mapping supplies both labels; Queryable Resource Attributes is met only with `keep_identifying_resource_attributes` enabled; Non-Breaking Server Compatibility is met trivially.

# Consensus

Consensus has not been reached. This section records each stakeholder's position and the reasoning behind it, so the disagreement is legible and the remaining decisions are explicit. Positions below are their authors' own.

## Arve Knudsen: Option C, with Variant C.1 as fallback

Preferred: Option C — declared-first precedence on the entity-less Prometheus path, with `prometheus.job` and `prometheus.instance` as Prometheus-side provenance and its identity-label fallback for Resources that declare nothing. Where a valid EntityRef set is present, the planned entity-aware mapping synthesizes from the complete Resource identity and no verbatim source overrides it. Acceptable fallback: Variant C.1, if covered-name recognition on `target_info` is rejected; the legacy precedence question matters more than the recognition question, and C.1 keeps declared-first while conceding recognition.

&nbsp;

Reasoning:

* Scrape target **`job` and `instance` coordinates should not replace an application's identity.** In the Entity model they do contribute to Resource identity when they are raw or referenced by `id_keys`; Option C instead recommends referencing them through `description_keys` when an application entity is present, and using them to identify a scrape-target entity where nothing else has been declared. Pair-first precedence asserts scrape authority universally.
* **Metadata consumers depend on the pair representing the Resource.** `target_info` is joined on `job` and `instance` labels: PromQL's `info()` function hard-codes them, and they are the only labels a classic join can rely on unless an operator promotes attributes expressly to match on instead. So identity cannot generally be relocated into metadata — the pair is the key through which metadata is reached. On the entity-less path, the question is whether that key derives from the covered declaration or from where Prometheus found it; on the entity-aware path, it derives from complete Resource identity.  
* **Declared-first overwrites no legacy label source.** Pair-first does not eliminate overwriting, it inverts it: Observed scrape coordinates displace an application's covered declaration, the mirror image of Practical Issue 1\. Declared-first is the only order under which the entity-less Prometheus mapping keeps whichever covered values were asserted, with the pair filling the label gap when none was. A partial declaration is kept on the same principle: a rule that completed it from the scrape pair would override the one value the Resource did declare, and that class needs no repair — today's output already carries whichever label its declaration produces, `job` for a declared `service.name` and `instance` for a declared `service.instance.id`. Canonical Resource identity itself is compositional, not precedence-based.  
* **Application identity should not be a property of monitoring configuration.** Deriving the key from the scrape configuration re-keys an application when its scrape job is renamed, and gives one application two identities when two Prometheus servers scrape it under different job names. Option C's fallback keys entity-less undeclared targets on those same coordinates, which is the best available where nothing was declared rather than a preference: The objection is to deriving the Prometheus key from the scrape configuration where a covered declaration exists. Entity-bearing path independence additionally requires the pair to be descriptive and the complete Resource identities to match.  
* **Retention is a separate axis from precedence.** Whether the covered attributes survive the hop as `target_info` labels is what `keep_identifying_resource_attributes` governs, and Section 2 already plans its default flip; B, C, and C.1 can each retain or drop them. A complaint that `service.*` gets stripped is an argument for that flip and for contrib parity — contrib's Remote Write translator removes all three unconditionally today — not for any precedence order here.  
* **It composes with the entity model without carve-outs.** Under the recommended policy, declared targets carry an application entity with the pair referenced descriptively, and undeclared ones may carry the scrape-target entity, so complete-Resource synthesis applies unchanged; a partial declaration supports neither and stays entity-less; under default derivation no entities are declared and today's translation stands. Declaring the scrape-target entity alongside an application entity does not override the application entity: Both entities, plus any raw attributes, contribute to Resource identity.  
* **Forward compatibility is a direction argument, not a present benefit.** The entity model formalizes the identity question rather than dissolving it — whichever design lands, something must define the complete identity of a scraped Resource — so a design that needs a verbatim carve-out to keep its defining property needs one permanently, where declared-first needs none. Option C also stands to gain: A scraped application and the same application pushing OTLP can land on one synthesized identity when they carry the same complete entity set and raw identifying attributes. The terms are worth stating plainly: The entity documents and the protocol's `Resource.entity_refs` field are at Development maturity, the concrete Prometheus mapping is planned rather than published, that convergence awaits a mechanism for relaying entity structure through exposition, and byte-exact `job`/`instance` output is lost for every class once entities are declared — Option C's undeclared targets included. The UUIDv5 `instance` assumed here derives from the complete Resource identity, preserving entity structure and including raw attributes while excluding entity descriptions; its canonical encoding remains for the Prometheus mapping to specify. The claim is that Option C is the option the future need not be rewritten to accommodate, not that it pays off today.

&nbsp;

Conceded: The declared-target shift changes the output `job` for targets whose exposition flattens the covered names, which is the populous path — underscore escaping is the Go SDK exporter's unconditional default. That cost is owned in Pros and Cons, and Variant C.1 exists to avoid it if the group judges it decisive. The change cuts both ways on that path: it breaks dashboards and rules keyed on the scrape string, and it repairs consumers that read service name and namespace out of `job`, a reading that holds today only where a scrape job happens to be named for its service. Under never-derive a declaration partial in `service.instance.id` also resolves to `job` alone, so replicas of such a service converge until its exporter sets that attribute.

&nbsp;

Asking the group to settle, in order:

&nbsp;

1. **Covered-name recognition** — whether a producer may read the three underscore forms on `target_info`. This separates C from C.1 and decides the populous flattened path.  
2. **Scope** — how much of the association and proof machinery has to land before anything ships, now that it sits in the appendix rather than in the contract.

&nbsp;

Regarding entity-less Prometheus label precedence (covered declaration or scrape target identity), Krajo's framing is the crux and I would like for him to say where it lands. If Prometheus is a resource detector making *its own* entity, then the application's entity is not replaced by the detector's — both entities contribute to the complete Resource identity — while Option C still rejects using the scrape pair verbatim ahead of the application declaration. If instead the scrape pair is the authoritative Prometheus key, that is Option B. Naming is closer to agreed: he dismisses Option A on the same provenance grounds and asks for `prometheus` semantic conventions defining `job` and `instance`, which is the reserved pair under another name.

## Krajo: long run option B

Reasoning:

* ~~I think the job and instance (whether assigned by Prometheus scrape or somebody else) does identify a resource. If it doesn't uniquely identify the resource, then something is set up wrong.~~  
  * ~~people using default translation already rely on this,~~  
  * ~~for a simple application where the resource is the same thing that is being scraped by a dedicated scrape config, this is trivially true,~~  
  * ~~for multi resource applications (what I call observer pattern), David's new suggested way of working applies \- add job/instance by the application and use `honor_labels` in the scrape config.~~  
  * ~~when the scrape config is something like a k8s discovery, people generally set up relabeling rules to make the job/instance be identifying (this is what we do in Grafana Cloud for example),~~  
  * ~~when we receive OTLP without job/instance (or prometheus.job/prometheus.instance) we'll have no choice but put something in job/instance that's identifying so that target\_info works (this would not be necessary if native metadata was working)~~  
  * ~~also note that in options C, C1, where we seed job/instance from [service.name/service.namespace/service.instance.id](http://service.name/service.namespace/service.instance.id) or entities, we're ensuring that they are identifying.~~  
* ~~When correlating signals, or when coming from outside Prometheus then job and instance are not great as they do not follow any semantic convention. It would be much more natural for a user to be able to use [service.name](http://service.name), [service.instance.id](http://service.instance.id) or whatever entity they have as a way to find metrics. (Native metadata would help achieve this, or if we promoted identifying attributes all the time). In short: it's kind of weird you have to know that for metrics the [service.name](http://service.name) is somewhere else. We're working around a bad, unintuitive user experience.~~  
* ~~So B is the desired state I think, to me the question is more about how we get there.~~  
* After in person discussion: although I still think that in *theory* it would have been pleasing to separate job+instance from [service.name/instane](http://service.name/instane) and other identities, it is not practical.  
  * First of all, for the eventual time series to be as identified (or as distinct) as the OTel resource identifier, the job+instance must be constructed to be as identifying as the OTel identifier. So you have to make sure that job+instance identifies the same resource. The simplest way is to derive them from the resource identity, not try to mimic them.  
  * Second: if and when we have native metadata support in Prometheus, technically we won't need the job+instance, a single uuid or any other label that identifies the resource will do.  
    * This would make it unnecessary to synthesize the job+instance in cases when the OTLP payload doesn't describe a prometheus target. In this sense it doesn't really matter what we put in the job+instance. Using job+instance is basically a workaround to get joins to work with target\_info.  
  * Option C makes the most effort to derive an identity into the job+instance from the resource attributes.

&nbsp;

## Jack Berg: Option C, with fallback treating all resource attributes as identifying

&nbsp;

Option C as written:

&nbsp;

1. If entities are present: `job=""`, `instance` \= hash of the entity's identifying attributes.  
2. Else if `service.name`/`service.instance.id` are present: derive `job`/`instance` as today.  
3. Else if `prometheus.job`/`prometheus.instance` are present: use verbatim.

&nbsp;

B vs C is a tradeoff between:

&nbsp;

* **B**: better cardinality control (`job`/`instance` fixed by scrape config), worse backwards compatibility (stepwise change in what `job`/`instance` mean, blocked until a major version and abrasive even then).  
* **C**: better backwards compatibility (preserves the invariant that `job`/`instance` represent the resource, which users already depend on), but leaves a gap: data with no entities, no `service.*`, and no scrape pair produces no identity at all. We can't guarantee that all receivers are entity-aware, nor that all of them refrain from applying `service.*` semantics to non-service telemetry sources. So this case will occur.

&nbsp;

Two ways to handle the "no identity" issue:

&nbsp;

* **Leave it**: identity-less data. Series land with empty `job`/`instance` and no reliable disambiguation.  
* **Add rule 4**: `job=""`, `instance` \= hash of all resource attributes. Risks cardinality churn when non-entity-aware resources carry high-churn descriptive attributes.

&nbsp;

I advocate for rule 4\. The cardinality risk is manageable:

&nbsp;

1. **Probably not that big a problem in practice**. Non-entity-aware resources usually already carry a high-cardinality identifying attribute (`service.instance.id`, container ID, etc.) that dominates the hash. High-cardinality descriptive attributes like `process.command_args` don't materially change churn when a UUID-shaped identifier is already present.  
2. **Solvable at the source**. Cases where churn does become a problem can be fixed by making the responsible collector receivers entity-aware. Bounded work, right incentive structure.

&nbsp;

Better to have potentially high-cardinality identity than no identity.

&nbsp;

The algorithm I advocate:

&nbsp;

1. If entities are present: `job=""`, `instance` \= hash of the entity's identifying attributes.  
2. Else if `service.name`/`service.instance.id` are present: derive as today.  
3. Else if `prometheus.job`/`prometheus.instance` are present: use verbatim.  
4. Else: `job=""`, `instance` \= hash of resource attributes.

&nbsp;

## David Ashpole: Option B (or A), otherwise option D

&nbsp;

Option C (and C1) are non-starters, as they violate one of the core requirements of the compatibility specification: job and instance round trip from Prometheus \-\> OTLP \-\> Prometheus. I would block any change which does not maintain that invariant. If we do not reach consensus on option B, I will proceed with fixing [https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/50502](https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/50502) to bring the implementation into compliance with the specification.

# Appendix side by side compare OUTDATED

See instead: [https://github.com/krajorama/oteljob](https://github.com/krajorama/oteljob)&nbsp;

&nbsp;

# ~~End-to-End Comparison: One Counter, Default Translation Strategy~~

~~The baseline single-application chain: one SDK application, counter `foo`, attribute `a.a="b"`, resource `service.name="my_service"` and `service.instance.id="my_id"`, scraped as `job="my_job"`, `instance="10.0.0.5:8080"`, with the SDK's Prometheus exporter left on its **default** translation strategy. This is what an application that configures nothing actually produces, so it is the most common shape in the wild — which is why it comes first. The `NoTranslation` variant of the same chain follows, and differs more than you would expect.~~

&nbsp;

~~The default resolves unconditionally to `UnderscoreEscapingWithSuffixes` in `newConfig` (`exporters/prometheus/config.go`). Note the `WithTranslationStrategy` doc comment describes a default that varies with `model.NameValidationScheme` — no code reads that variable, so the comment is stale.~~

&nbsp;

~~Two consequences make this scenario behave quite differently from the first, before any option is considered:~~

&nbsp;

* **~~The exposition is negotiation-independent.~~** ~~The exporter escapes names at the OTel→Prometheus translation layer, before `client_golang`'s exposition escaping ever runs. The bytes are byte-identical under all four negotiated schemes — `allow-utf-8`, `underscores`, `dots` and `values` — verified against the exporter for each. Nothing is left for the exposition layer to escape, so the escaping coin flip that decides the `NoTranslation` scenario cannot arise here for Today, A or B.~~  
* **~~The counter type survives.~~** ~~Suffixes are on, so the family is `foo_total`, and OpenMetrics types it `counter` rather than degrading to `unknown`. This is the `_total` naming point from note (g) of the `NoTranslation` scenario, reached automatically by the default.~~

&nbsp;

~~Shorthands: **\[enrich\]** and **\[scope\]** as before, with `[scope]` \= `otel_scope_name="my.scope", otel_scope_version="v1.0.0"`.~~

&nbsp;

| ~~Step~~ | ~~Today~~ | ~~Option A — bare~~ | ~~Option B — namespaced~~ | ~~Option C — declared-first~~ |
| :---- | :---- | :---- | :---- | :---- |
| **~~1\. OTel metric data model~~** | ~~`Resource{service.name="my_service", service.instance.id="my_id"}` `Scope{name="my.scope", version="v1.0.0"}` `Metric{name="foo", type=Sum, monotonic=true, temporality=cumulative}`   `DataPoint{attributes={"a.a": "b"}, value=42}`~~ | ~~Identical~~ | ~~Identical~~ | ~~Identical~~ |
| **~~2\. SDK exposition~~** ~~(default strategy, OpenMetrics 1.0)~~ | ~~`# HELP foo a simple counter` `# TYPE foo counter` `foo_total{a_a="b",otel_scope_name="my.scope",otel_scope_schema_url="",otel_scope_version="v1.0.0"} 42.0` `# HELP target_info Target metadata` `# TYPE target_info gauge` `target_info{service_instance_id="my_id",service_name="my_service"} 1.0` `# EOF` Identical under `allow-utf-8` and `underscores` negotiation.~~ | ~~Identical~~ | ~~Identical~~ | ~~Identical~~ |
| **~~3\. Collector `prometheusreceiver` → OTLP~~** | ~~`Resource{service.name="my_job", service.instance.id="10.0.0.5:8080", service_name="my_service", service_instance_id="my_id", [enrich]}` `Scope{name="my.scope", version="v1.0.0"}` `Metric{name="foo_total", type=Sum, monotonic=true, metadata{prometheus.type="counter"}}`   `DataPoint{attributes={"a_a": "b"}, value=42}` **Nothing collides**, so nothing is overwritten: the scrape identity occupies `service.name`/`service.instance.id`, and the application's declaration sits alongside under the alias names. See note (c).~~ | ~~`Resource{job="my_job", instance="10.0.0.5:8080", service.name="my_job", service.instance.id="10.0.0.5:8080", service_name="my_service", service_instance_id="my_id", [enrich]}` Scope, Metric, DataPoint identical to Today. The Core Rules do not un-escape the aliases, so `service.*` are still MAY-defaulted from the pair.~~ | ~~`Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service_name="my_service", service_instance_id="my_id", [enrich]}` Scope, Metric, DataPoint identical to Today.~~ | ~~`Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service.name="my_job", service.instance.id="10.0.0.5:8080", service_name="my_service", service_instance_id="my_id", [enrich]}` Scope, Metric, DataPoint identical to Today.The result depends on the negotiated escaping, even though the exposition does not. See note (d). allow-utf-8 negotiated (the scrape default) — the profile expects dotted names, so service\_name/service\_instance\_id are not recognized as aliases and stay ordinary metadata. No covered attributes reach the receiver, so this is an undeclared target and service.\* are derived from the pair as today: Resource{prometheus.job="my\_job", prometheus.instance="10.0.0.5:8080", service.name="my\_job", service.instance.id="10.0.0.5:8080", service\_name="my\_service", service\_instance\_id="my\_id", \[enrich\]} — identical to B, attribute for attribute. underscores negotiated — the profile decodes the aliases, and recognized aliases are consumed rather than retained, so this is a declared target: Resource{prometheus.job="my\_job", prometheus.instance="10.0.0.5:8080", service.name="my\_service", service.instance.id="my\_id", \[enrich\]} Scope, Metric and DataPoint are identical to Today in both branches.~~ |
| **~~4\. PRW exporter → series~~** | ~~`foo_total{job="my_job", instance="10.0.0.5:8080", a_a="b", [scope]} 42` `target_info{job="my_job", instance="10.0.0.5:8080", service_name="my_service", service_instance_id="my_id", [enrich]} 1` `job`/`instance` are the scrape's, and the application's declaration rides `target_info` — the best outcome available today.~~ | ~~`foo_total{job="my_job", instance="10.0.0.5:8080", a_a="b", [scope]} 42` — byte-identical to Today. `target_info` depends on how note (i) of the `NoTranslation` scenario is read. Keeping today's suppression of `service.name`/`service.instance.id` gives Today's line unchanged. Following that note instead puts all four attributes in play, and the dotted and alias forms translate to one label each, which `createAttributes` joins with `;`: `target_info{job="my_job", instance="10.0.0.5:8080", service_name="my_job;my_service", service_instance_id="10.0.0.5:8080;my_id", [enrich]} 1`~~ | ~~Byte-identical to A, both readings.~~ | ~~`allow-utf-8`: `foo_total{job="my_job", instance="10.0.0.5:8080", a_a="b", [scope]} 42` — byte-identical to Today. `target_info{job="my_job", instance="10.0.0.5:8080", prometheus_job="my_job", prometheus_instance="10.0.0.5:8080", service_name="my_service", service_instance_id="my_id", [enrich]} 1` `underscores`: the declaration governs, so identity shifts to the application's values: `foo_total{job="my_service", instance="my_id", a_a="b", [scope]} 42` `target_info{job="my_service", instance="my_id", prometheus_job="my_job", prometheus_instance="10.0.0.5:8080", [enrich]} 1`~~ |
| **~~Net effect~~** | ~~The scrape identity round-trips byte-exactly *and* the application's declaration survives as `target_info` metadata. Nothing is lost. The cost is Practical Issue 3: the OTel semantic identity now holds scrape-config strings, and the real service name is metadata rather than identity.~~ | ~~Same output values as Today; `job`/`instance` gain an explicit OTLP home. Under that note's reading, `target_info` regresses from two clean labels to two concatenations.~~ | ~~Same as A.~~ | ~~Under the default negotiation, indistinguishable from Today except for two duplicate `target_info` labels.  Under `underscores` negotiation, **BREAKING** — `job`/`instance` shift from the scrape to the application. Byte-identical input, two different answers.~~ |

&nbsp;

**~~Notes~~**

&nbsp;

* **~~(a) This is the scenario where today's behavior is already good.~~** ~~Unlike the `NoTranslation` scenario, no identity is destroyed: the collision that forces a choice there cannot happen here, because the exporter pre-escapes the covered names into `service_name`/`service_instance_id` and the receiver's job-derived `service.name`/`service.instance.id` occupy different keys. Both identities reach OTLP, and both reach Prometheus. The problem this document sets out to solve is therefore *not* a problem on the default-strategy path — it is a problem on the `NoTranslation`/UTF-8 path, and it is a naming-hygiene problem here.~~  
* **~~(b) …which is exactly Practical Issue 3\.~~** ~~`service.name="my_job"` is a scrape-config string sitting in the attribute that OTel-native consumers group by, while the application's real identity is one alias attribute away. Options A, B and C-under-`underscores` all fix this by giving the pair its own home; C-under-`allow-utf-8` does not.~~  
* **~~(c) The alias attributes are inert metadata to every consumer.~~** ~~`service_name` and `service_instance_id` are ordinary Resource attributes: no OTel semantic convention refers to them, no consumer treats them as identity, and the PRW exporter emits them as ordinary `target_info` labels. They preserve the value without preserving its meaning — the "this just looks weird" observation from the Use case section, stated precisely.~~  
* **~~(d) Option C's mapping profile is keyed on the negotiated escaping, not on the producer's translation strategy — and here the two diverge.~~** ~~Covered Label Mapping says pull paths "use the negotiated Prometheus escaping scheme", and under `allow-utf-8` that profile expects dotted names. But the SDK's default strategy escapes at a different layer, so a scrape that negotiated `allow-utf-8` still receives `service_name`. C therefore misses the declaration under the scrape default and recognizes it only when the negotiation happens to match. This weakens note (h) of the `NoTranslation` scenario: C is escaping-independent only when the exposition's escaping follows the negotiation, which is true for `NoTranslation` and false for the default strategy. Remote Write already acknowledges the problem — "Producer and receiver profiles must match" — and pull paths need the same explicit control rather than trusting negotiation.~~  
* **~~(e) Verification.~~** ~~Steps 1–3 are verbatim. The step-2 exposition is from the Go SDK exporter with no `WithTranslationStrategy` option, captured under all four negotiated escaping schemes (byte-identical in every case). Step 3 is from contrib's `prometheusreceiver` scraping that exposition as OpenMetrics 1.0 — `service.name="my_job"`, `service.instance.id="<host:port>"`, `service_name="my_service"`, `service_instance_id="my_id"`, `Metric{name="foo_total", type=Sum, metadata[prometheus.type:counter]}`, `DataPoint{attrs=map[a_a:b]}`. Step 4 and the option columns are derived from `createAttributes`/`addResourceTargetInfo` and the proposal texts.~~

# ~~End-to-End Comparison: One Counter, `NoTranslation` Strategy~~

~~A single concrete payload traced through every hop, so the data model at each step can be compared side by side. The scenario is the SDK-behind-a-scrape case from [Use case](https://docs.google.com/document/d/1QT4wlNv4XOasuPPIIbfBFv5kL_azyhEt_UiFNl4VEF0/edit#use-case), with all defaults except the SDK's translation strategy. The same payload with the *default* strategy behaves quite differently and is traced separately — see [One Counter, Default Translation Strategy](https://docs.google.com/document/d/1QT4wlNv4XOasuPPIIbfBFv5kL_azyhEt_UiFNl4VEF0/edit#end-to-end-comparison-one-counter-default-translation-strategy).~~

&nbsp;

* **~~Application~~**~~: OTel Go SDK, one Int64Counter named `foo` with attribute `a.a="b"`, resource attributes `service.name="my_service"` and `service.instance.id="my_id"`, scope `my.scope` version `v1.0.0`.~~  
* **~~SDK exporter~~**~~: `go.opentelemetry.io/otel/exporters/prometheus` with `WithTranslationStrategy(otlptranslator.NoTranslation)`; everything else default (`target_info` on, scope labels on).~~  
* **~~Scrape~~**~~: the collector's `prometheusreceiver` with default `scrape_protocols` (OpenMetrics 1.0.0 first) and default `metric_name_escaping_scheme: allow-utf-8`. The target is configured as `job="my_job"`, `instance="10.0.0.5:8080"`, scheme `http`.~~  
* **~~Egress~~**~~: the collector's `prometheusremotewrite` exporter with defaults (`add_metric_suffixes: true`, `target_info.enabled: true`, Remote Write 1.0, no `promote_resource_attributes` equivalent exists in contrib).~~  
* **~~Options~~**~~: each option column assumes producer emission enabled and `service.*` defaulting left at its default (never-derive not opted in for C). Defaulting never fires in this scenario anyway — `target_info` supplies both covered attributes.~~

&nbsp;

~~Two shorthands keep the cells readable: **\[enrich\]** is the receiver-added target context, `server.address="10.0.0.5", server.port="8080", url.scheme="http"` in OTLP and `server_address`/`server_port`/`url_scheme` as labels; **\[scope\]** is `otel_scope_name="my.scope", otel_scope_version="v1.0.0"`.~~

&nbsp;

**~~The `allow-utf-8` premise is doing most of the work here.~~** ~~With `NoTranslation` the exporter escapes nothing, so the exposed spelling of the covered names is decided entirely by the scrape's negotiated `metric_name_escaping_scheme`. All four schemes, verified against the exporter:~~

&nbsp;

| ~~negotiated escaping~~ | ~~`target_info` as exposed~~ | ~~Collides with the receiver's job-derived `service.name`?~~ |
| :---- | :---- | :---- |
| ~~`allow-utf-8` (the scrape default)~~ | ~~`target_info{"service.instance.id"="my_id","service.name"="my_service"} 1.0`~~ | **~~Yes~~** ~~— identical keys~~ |
| ~~`underscores`~~ | ~~`target_info{service_instance_id="my_id",service_name="my_service"} 1.0`~~ | ~~No~~ |
| ~~`dots`~~ | ~~`target_info{service_dot_instance_dot_id="my_id",service_dot_name="my_service"} 1.0`~~ | ~~No~~ |
| ~~`values`~~ | ~~`target_info{U__service_2e_instance_2e_id="my_id",U__service_2e_name="my_service"} 1.0`~~ | ~~No~~ |

&nbsp;

~~Only the first row collides, and only a collision forces the receiver to choose between the two identities. In the other three the declaration lands under a different key and both identities survive — so **the problem this document exists to solve requires `NoTranslation` (or `NoUTF8EscapingWithSuffixes`) *and* `allow-utf-8`**. The table below traces that first row; note (h) covers the rest, and the `underscores` row is worked through in full as its own use case, since it coincides exactly with the default strategy's outcome.~~

&nbsp;

| ~~Step~~ | ~~Today (current SDK \+ collector)~~ | ~~Option A — bare `job`/`instance`~~ | ~~Option B — `prometheus.job`/`prometheus.instance`~~ | ~~Option C — namespaced, declared-first~~ |
| :---- | :---- | :---- | :---- | :---- |
| **~~1\. OTel metric data model~~** ~~(in-process, SDK)~~ | ~~`Resource{service.name="my_service", service.instance.id="my_id"}` `Scope{name="my.scope", version="v1.0.0", schema_url=""}` `Metric{name="foo", unit="", type=Sum, monotonic=true, temporality=cumulative}`   `DataPoint{attributes={"a.a": "b"}, value=42}`~~ | ~~Identical — see note (f)~~ | ~~Identical~~ | ~~Identical~~ |
| **~~2\. SDK Prometheus exporter exposition~~** ~~(`NoTranslation`, OpenMetrics 1.0, `escaping=allow-utf-8`)~~ | ~~`# HELP foo a simple counter` `# TYPE foo unknown` `foo{"a.a"="b",otel_scope_name="my.scope",otel_scope_schema_url="",otel_scope_version="v1.0.0"} 42.0` `# HELP target_info Target metadata` `# TYPE target_info gauge` `target_info{"service.instance.id"="my_id","service.name"="my_service"} 1.0` `# EOF` **Note**: the type is `unknown`, not `counter` — see note (g).~~ | ~~Identical — see note (f)~~ | ~~Identical~~ | ~~Identical~~ |
| **~~3\. Collector `prometheusreceiver` → OTLP~~** | ~~`Resource{service.name="my_service", service.instance.id="my_id", [enrich]}` `Scope{name="my.scope", version="v1.0.0", schema_url=""}` `Metric{name="foo", type=Gauge, metadata{prometheus.type="unknown"}}`   `DataPoint{attributes={"a.a": "b"}, value=42}` **`job`/`instance` survive nowhere.** `CreateResource` seeds `service.name`/`service.instance.id` from `job`/`instance`, then `AddTargetInfo` `PutStr`s every `target_info` label except `job`/`instance`/`__name__` over them. Under `allow-utf-8` the dotted names collide exactly, so the declaration wins and the scrape identity is destroyed; only `[enrich]` partially witnesses the target address. Under the other three escaping schemes nothing collides and both identities survive — for `underscores` that Resource is the default strategy's, traced in its own use case.~~ | ~~`Resource{job="my_job", instance="10.0.0.5:8080", service.name="my_service", service.instance.id="my_id", [enrich]}` Scope, Metric, DataPoint identical to Today. Both identities stored side by side; nothing overwrites anything. Escaping-dependent — see note (h).~~ | ~~`Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service.name="my_service", service.instance.id="my_id", [enrich]}` Scope, Metric, DataPoint identical to Today. Same as A modulo the prefix. Escaping-dependent — see note (h).~~ | ~~`Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service.name="my_service", service.instance.id="my_id", [enrich]}` Scope, Metric, DataPoint identical to Today. Attribute set identical to B; the *roles* differ — the pair is descriptive provenance, the covered attributes are the declared identity. Escaping-**in**dependent: the mapping profile decodes `service_name`/`service_instance_id` too.~~ |
| **~~4\. Collector `prometheusremotewrite` exporter → Prometheus series~~** | ~~`foo{job="my_service", instance="my_id", a_a="b", [scope]} 42` `target_info{job="my_service", instance="my_id", [enrich]} 1`~~ | ~~`foo{job="my_job", instance="10.0.0.5:8080", a_a="b", [scope]} 42` `target_info{job="my_job", instance="10.0.0.5:8080", service.name="my_service", service.instance.id="my_id", [enrich]} 1` Pair-first lookup: the stored pair is consumed as identity, so it is not additionally emitted under its own name. See notes (i) and (j).~~ | ~~Byte-identical to A. The pair is consumed as `job`/`instance` labels, so `prometheus_job`/`prometheus_instance` do not appear. Precedence is unspecified in Section 3 — see note (k).~~ | ~~`foo{job="my_service", instance="my_id", a_a="b", [scope]} 42` — **bit-identical to Today** `target_info{job="my_service", instance="my_id", prometheus_job="my_job", prometheus_instance="10.0.0.5:8080", [enrich]} 1` Declared identity governs; the pair rides `target_info` as descriptive metadata. `service_name`/`service_instance_id` stay suppressed — see note (i).~~ |
| **~~Net effect on the Prometheus series~~** | ~~Scrape identity destroyed. The series is keyed by the declaration under labels the scraper never used, so dashboards and rules written against the scrape config do not survive the OTLP hop. Practical Issue 1, in the direction where the declaration wins.~~ | ~~Byte-exact scrape-identity round trip, and `service.*` become queryable on `target_info`. But `foo`'s `job`/`instance` values change relative to today → **BREAKING** (case O5).~~ | ~~Same break as A on the wire; the only difference is that OTTL and processors must target `prometheus.job`, not `job`.~~ | ~~No break: `foo` is bit-identical to today, and the scrape identity is one `target_info` join away. `target_info` gains two labels once at adoption. The cost is that the declared-target output `job`/`instance` still differ from the original scrape labels — the deliberate declared-target shift.~~ |

&nbsp;

**~~Notes~~**

&nbsp;

* **~~(f) Steps 1 and 2 are option-independent.~~** ~~All three options change only the Prometheus↔OTLP translation. The application's Resource carries no `job`/`instance` (or `prometheus.*`) pair to relay, and the SDK's Prometheus exporter is a single-resource pull exporter that never stamps `job`/`instance` — the scraper supplies them, and with `honor_labels: false` (the scrape default) the scraper's values would win regardless. So the exposition is byte-identical in all four columns.~~  
* **~~(g) The counter → Gauge loss is option-independent too.~~** ~~`NoTranslation` forcibly disables the `_total` suffix, and the OpenMetrics encoder writes `unknown` for a counter family whose name does not end in `_total` (`prometheus/common/expfmt/openmetrics_create.go`). The receiver then maps `unknown` → Gauge, and step 4 re-emits a gauge without `_total`; `prometheus.type="unknown"` is not consulted and `send_metadata` is off by default. This is a `NoTranslation` \+ OpenMetrics artifact, orthogonal to the job/instance question, but it means the round trip is not type-preserving in any column.~~  
* **~~(h) A and B stay escaping-dependent; C mostly does not, and is strongest where A and B are worst.~~** ~~The table fixes `escaping=allow-utf-8`; the matrix above gives the other three. Under `underscores`, `dots` or `values`, `target_info` carries an escaped spelling of the covered names, which the Core Rules do not un-escape — so A and B do not recognize a declaration, `service.name` is MAY-defaulted from `job` (`"my_job"`), and the declaration sits alongside as an unrelated attribute under whatever key the escaping produced. Today's behavior under those schemes is the same, minus the stored pair. C's Covered Label Mapping decodes the aliases instead, so its step-3 Resource is the same under `underscores`, `dots` and `values` — and `dots`/`values` are the schemes where that pays off most, because they are unambiguously reversible: under `underscores` C cannot distinguish a translated `service.name` from an application that genuinely declared an attribute called `service_name`. The `dots` and `values` rows are therefore the only ones where the declaration survives *as a declaration* under any option — for A and B it survives as an unreadable key (`service_dot_name`, `U__service_2e_name`) that no consumer interprets. Note (d) of the default-strategy scenario covers the case where C's profile selection misfires.~~  
* **~~(i) The `target_info` label sets diverge.~~** ~~To satisfy Requirement 3, A and B must stop suppressing `service.name`/`service.instance.id` — contrib's `addResourceTargetInfo` currently passes all three covered attributes as `ignoreAttrs` — so `service_name`/`service_instance_id` appear. Under C they remain the identity source and stay suppressed unless `keep_identifying_resource_attributes` is set, which contrib does not implement, so only the two pair labels are added. In every column `target_info` labels are underscore-escaped regardless of the configured translation strategy, because `addResourceTargetInfo` builds its own `LabelNamer` without `UTF8Allowed`.~~  
* **~~(j) If step 4 were Prometheus's own OTLP endpoint instead.~~** ~~The contrib exporter implements pair lookup directly, as shown above. Server-side, A and B need `honor_labels: true` on the OTLP endpoint (Section 2); with the current default of `false` the A and B columns collapse onto the Today column. C needs no flag — its output is what the endpoint already produces for a Resource with a declared identity.~~  
* **~~(k) B's consumer precedence is unspecified.~~** ~~Section 3 defines B only as A's storage names with a prefix, and the Comparison with Options A and B table marks its pair role and consumer activation "Unspecified". The B column above reads it as pair-first, i.e. A's semantics with a prefix. Were B instead specified declared-first, its column would become identical to C's — the prefix alone is not what separates B from C.~~  
* **~~(l) Dotted names.~~** ~~`a.a` is preserved on the wire at step 2 and in OTLP at step 3 in all columns, then escaped to `a_a` at step 4 because the exporter's default strategy is underscore-escaping with suffixes. The escaping choice at step 3 flips *which* identity is lost today, not *whether* one is.~~

# ~~End-to-End Comparison: An Observer Exposing Metrics About Other Services~~

~~The same chain, but the exposing service reports on services it *observes* rather than on itself — a blackbox prober, a cloud-API poller, a database exporter. This is the case [spec PR 4956](https://github.com/open-telemetry/opentelemetry-specification/pull/4956) names an [**aggregated exporter**](https://github.com/dashpole/opentelemetry-specification/blob/prom_stabilize_resource/specification/compatibility/prometheus_and_openmetrics.md#aggregated-exporters); the published compatibility specification has no term for it and covers it only as a property of the Collector's Prometheus exporters, under [Resource Attributes](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/#resource-attributes-1). It is where the identity question stops being cosmetic.~~

&nbsp;

~~The OTel Go SDK has no example for this shape and cannot really express it. The Prometheus exporter reads a single `*resource.Resource` per `MeterProvider`, so N observed targets means N `MeterProvider`s sharing one registry. Doing that naively fails at `Gather()`: the two targets' series are label-identical, so the registry rejects the duplicate (`collected metric "foo" ... was collected before with the same name and label values`) — only `target_info` fans out, because its labels differ. The one lever that makes it gather is `WithResourceAsConstantLabels`, which promotes selected resource attributes onto every series. That is what this scenario uses:~~

&nbsp;

* **~~Observer~~**~~: two `MeterProvider`s on one registry, resources `{service.name="observed_service", service.instance.id="observed_a", region="eu"}` and `{..."observed_b", region="us"}`. Each records the same counter `foo` with attribute `a.a="b"`, scope `obs`.~~  
* **~~SDK exporter~~**~~: `WithTranslationStrategy(otlptranslator.NoTranslation)` plus `WithResourceAsConstantLabels(attribute.NewAllowKeysFilter("service.instance.id"))`. Note this option deliberately "does not affect the target info generated from resource attributes", so `target_info` still fans out per resource.~~  
* **~~Scrape and egress~~**~~: as in the previous scenario — `job="my_job"`, `instance="10.0.0.5:8080"`, scheme `http`, default exporter settings.~~

&nbsp;

~~Shorthands: **\[enrich\]** as before; **\[scope\]** is just `otel_scope_name="obs"` here (the scope has no version, and empty label values are dropped).~~

&nbsp;

~~Step 2 is the crux — one endpoint, two `target_info` series, and no per-resource `job`/`instance` anywhere.~~

&nbsp;

| ~~Step~~ | ~~Today~~ | ~~Option A — bare~~ | ~~Option B — namespaced~~ | ~~Option C — declared-first~~ |
| :---- | :---- | :---- | :---- | :---- |
| **~~1\. OTel data model~~** ~~(observer process)~~ | ~~Two Resources: `{service.name="observed_service", service.instance.id="observed_a", region="eu"}` and `{..."observed_b", region="us"}`, each with `Metric{name="foo", type=Sum, monotonic}` / `DataPoint{{"a.a":"b"}, 42}`~~ | ~~Identical~~ | ~~Identical~~ | ~~Identical~~ |
| **~~2\. SDK exposition~~** ~~(`NoTranslation`, OpenMetrics 1.0, `escaping=allow-utf-8`)~~ | ~~`# HELP foo a simple counter` `# TYPE foo unknown` `foo{"a.a"="b",otel_scope_name="obs",otel_scope_schema_url="",otel_scope_version="","service.instance.id"="observed_a"} 42.0` `foo{"a.a"="b",otel_scope_name="obs",otel_scope_schema_url="",otel_scope_version="","service.instance.id"="observed_b"} 42.0` `# HELP target_info Target metadata` `# TYPE target_info gauge` `target_info{region="eu","service.instance.id"="observed_a","service.name"="observed_service"} 1.0` `target_info{region="us","service.instance.id"="observed_b","service.name"="observed_service"} 1.0` `# EOF` Two `target_info` series share one endpoint, and the observed identity rides the ordinary label `service.instance.id` — there is no `job` or `instance` anywhere.~~ | ~~Identical~~ | ~~Identical~~ | ~~Identical~~ |
| **~~3\. Receiver → OTLP~~** | **~~One~~** ~~Resource for the single `(job, instance)` pair, both `target_info` series `PutStr` onto it — **last one wins**. `utf-8`: `service.name="observed_service"`, `service.instance.id="observed_b"`, `region="us"`, `[enrich]` — the observer's own `my_job`/`10.0.0.5:8080` are overwritten too, and `region="eu"` is gone. `underscores`: `service.name="my_job"`, `service.instance.id="10.0.0.5:8080"`, plus `service_name="observed_service"`, `service_instance_id="observed_b"`, `region="us"`, `[enrich]`. Both datapoints survive, distinguished only by the point attribute `service.instance.id`.~~ | **~~One~~** ~~Resource, as Today. `utf-8`: `Resource{job="my_job", instance="10.0.0.5:8080", service.name="observed_service", service.instance.id="observed_b", region="us", [enrich]}` `underscores`: `Resource{job="my_job", instance="10.0.0.5:8080", service.name="my_job", service.instance.id="10.0.0.5:8080", service_name="observed_service", service_instance_id="observed_b", region="us", [enrich]}` The pair survives, but the covered attributes are still last-wins — the Core Rules define no merge rule for multiple `target_info` series sharing one pair, so `region="eu"` is lost either way.~~ | **~~One~~** ~~Resource, same values as A with the names prefixed. `utf-8`: `Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service.name="observed_service", service.instance.id="observed_b", region="us", [enrich]}` `underscores`: `Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service.name="my_job", service.instance.id="10.0.0.5:8080", service_name="observed_service", service_instance_id="observed_b", region="us", [enrich]}`~~ | **~~One~~** ~~Resource, and the same one in both escapings: `Resource{prometheus.job="my_job", prometheus.instance="10.0.0.5:8080", service.name="observed_service", [enrich]}` The agreement rules fire: `service.name` agrees across both contributors and is retained; `service.instance.id` (`observed_a` vs `observed_b`) and `region` (`eu` vs `us`) disagree and are **omitted**, one bounded diagnostic each. Under `underscores` the mapping profile decodes the aliases before merging, so the result is the same. Note that C specifies no per-key completion, so the omitted `service.instance.id` is *not* re-derived from the pair.~~ |
| **~~4\. PRW exporter → series~~** | ~~`utf-8`: `foo{job="observed_service", instance="observed_b", a_a="b", service_instance_id="observed_a"|"observed_b", [scope]} 42` `target_info{job="observed_service", instance="observed_b", region="us", [enrich]} 1` `underscores`: `foo{job="my_job", instance="10.0.0.5:8080", a_a="b", service_instance_id="observed_a"|"observed_b", [scope]} 42` `target_info{job="my_job", instance="10.0.0.5:8080", service_name="observed_service", service_instance_id="observed_b", region="us", [enrich]} 1`~~ | ~~Pair-first, so the ordinary series are the same under either escaping: `foo{job="my_job", instance="10.0.0.5:8080", a_a="b", service_instance_id="observed_a"|"observed_b", [scope]} 42` `utf-8`: `target_info{job="my_job", instance="10.0.0.5:8080", service_name="observed_service", service_instance_id="observed_b", region="us", [enrich]} 1` `underscores`: the Resource now holds both `service.name` and `service_name`, which translate to the same label, and `createAttributes` joins colliding values with `;`: `target_info{job="my_job", instance="10.0.0.5:8080", service_name="my_job;observed_service", service_instance_id="10.0.0.5:8080;observed_b", region="us", [enrich]} 1` A consequence of note (i) of the `NoTranslation` scenario.~~ | ~~Byte-identical to A under both escapings.~~ | ~~Declared-first, and `service.name` survived the merge, so it governs. Same under both escapings: `foo{job="observed_service", a_a="b", service_instance_id="observed_a"|"observed_b", [scope]} 42` `target_info{job="observed_service", prometheus_job="my_job", prometheus_instance="10.0.0.5:8080", [enrich]} 1` There is **no `instance` label at all**: the conflicting `service.instance.id` was omitted upstream, and `createAttributes` only sets `instance` when the attribute exists — where the spec would instead emit it present-but-empty. `region` is gone for the same reason.~~ |
| **~~Net effect~~** | ~~Silent corruption. Under `utf-8` the Resource claims to *be* `observed_b` and the observer's identity is gone; under `underscores` the observer's identity survives but the observed metadata is still last-wins. One observed target's `region` is lost in both.~~ | ~~The observer's scrape identity round-trips byte-exactly, which is the honest provenance for an aggregated endpoint, and the observed identity remains queryable as a point attribute. The per-target metadata corruption is unchanged.~~ | ~~Same as A; only the OTTL target name differs.~~ | ~~The conflict is **detected rather than absorbed** — the one column that does not silently publish a wrong `service.instance.id`. But the resulting identity is the weakest of the four: `job` from the observed service, and no `instance` label at all.~~ |

&nbsp;

**~~Notes~~**

&nbsp;

* **~~(m) They do not all agree — and the split is not the one the `NoTranslation` scenario showed.~~** ~~Under `underscores`, Today, A and B produce identical output and only C diverges. Under `allow-utf-8`, A and B diverge from Today, and C diverges from all three. C's divergence has two independent causes: its mapping profile *recognizes* `service_name`/`service_instance_id` as a declaration where Today/A/B leave them as unrelated attributes, and its contributor-agreement rule drops conflicting keys.~~  
* **~~(n) This is the one scenario where A beats C on output quality.~~** ~~For an aggregated endpoint, `job`/`instance` describing the observer is the truthful answer — it is the scrape provenance, and it is what `honor_labels: false` would have produced anyway. A and B deliver exactly that. C, by ranking a declared identity above the pair, hands `job` to the observed service and drops `instance` entirely. Worth stating explicitly in Pros and Cons: declared-first is the right precedence when the Resource describes the process that declared it, and the wrong one when the Resource describes something the process merely observes. Nothing in either proposal distinguishes those two cases.~~  
* **~~(o) No option fixes the actual defect.~~** ~~Two observed targets collapse into one OTel Resource because the pull exposition has no way to assert a per-resource `job`/`instance` — the scraper stamps one pair for the whole endpoint. A, B and C all govern what happens *after* that collapse. The fixes lie elsewhere: have the observer emit distinct `instance` labels per target and scrape it with `honor_labels: true` (the note under Use case already anticipates this), or model the observer as a Collector receiver that produces one Resource per observed target and let an aggregated exporter stamp identity per Resource.~~  
* **~~(p) Verification.~~** ~~The exposition in step 2 and the `Gather()` collision are verbatim from the Go SDK exporter. Step 3's `underscores` row is verbatim from the contrib `prometheusreceiver` (one `ResourceMetrics`, `region="us"`, `service_instance_id="observed_b"`, both datapoints present). The `utf-8` rows for steps 3 and 4, and all option columns, are derived from `AddTargetInfo`/`createAttributes` and the proposal texts.~~

# ~~End-to-End Comparison: A Proper Aggregation, With `job` and `instance` Exposed~~

~~The previous use case failed because the exposing service could not assert a `job`/`instance` pair per observed target. This one does it correctly: the Collector's own Prometheus **pull** exporter stamps `job` and `instance` on every exposed series and emits one `target_info` per Resource, and the downstream scraper is configured with `honor_labels: true` so those labels survive. This is the federation path, and it is the scenario where today's translation already round-trips.~~

&nbsp;

* **~~Origin~~**~~: two OTLP Resources reaching a collector — `{service.name="svc_a", service.instance.id="a1", region="eu"}` and `{service.name="svc_b", service.instance.id="b1", region="us"}` — each with cumulative monotonic `Sum` named `foo`, datapoint attribute `a.a="b"`, scope `obs` version `v1.0.0`. No `job`/`instance` (or `prometheus.*`) resource attributes: this is OTLP-native input.~~  
* **~~Aggregating exporter~~**~~: contrib `prometheusexporter` with defaults and `enable_open_metrics: true`.~~  
* **~~Re-scrape~~**~~: a downstream `prometheusreceiver` with `honor_labels: true`, target `collector:8889`.~~  
* **~~Egress~~**~~: `prometheusremotewrite` exporter with defaults.~~

&nbsp;

~~In step 2, note what is *not* on `target_info`: `service_name` and `service_instance_id`. `addTargetInfoMetric` unconditionally `RemoveIf`s `service.name`, `service.namespace` and `service.instance.id` before building the labels, on the grounds that they became `job`/`instance` — and the pull exporter has no `keep_identifying_resource_attributes` equivalent. See note (s).~~

&nbsp;

| ~~Step~~ | ~~Today~~ | ~~Option A — bare~~ | ~~Option B — namespaced~~ | ~~Option C — declared-first~~ |
| :---- | :---- | :---- | :---- | :---- |
| **~~1\. Origin OTLP~~** | ~~Two Resources, `{service.name, service.instance.id, region}` each; no pair present~~ | ~~Identical~~ | ~~Identical~~ | ~~Identical~~ |
| **~~2\. Pull exporter exposition~~** ~~(OpenMetrics 1.0)~~ | ~~`# HELP foo a simple counter` `# TYPE foo counter` `foo_total{a_a="b",instance="a1",job="svc_a",otel_scope_name="obs",otel_scope_schema_url="",otel_scope_version="v1.0.0"} 42.0` `foo_total{a_a="b",instance="b1",job="svc_b",otel_scope_name="obs",otel_scope_schema_url="",otel_scope_version="v1.0.0"} 42.0` `# HELP target_info Target metadata` `# TYPE target_info gauge` `target_info{instance="a1",job="svc_a",region="eu"} 1.0` `target_info{instance="b1",job="svc_b",region="us"} 1.0` `# EOF` `job`/`instance` on every series, one `target_info` per Resource, covered attributes stripped.~~ | ~~Identical — the origin carries no pair, so pair-first lookup falls through to the `service.*` derivation~~ | ~~Identical~~ | ~~Identical — declared identity governs, and there is no pair to ride `target_info`~~ |
| **~~3\. Re-scrape → OTLP~~** ~~(`honor_labels: true`)~~ | **~~Two~~** ~~Resources — correct separation. `getJobAndInstance` prefers the series labels over the scrape target, so `(svc_a, a1)` and `(svc_b, b1)` are distinct keys and each `target_info` associates with its own. `{service.name="svc_a", service.instance.id="a1", region="eu", server.address="a1", server.port="", url.scheme="http"}` and the `svc_b`/`b1`/`us` twin. `Metric{name="foo_total", type=Sum, monotonic}` / `DataPoint{{"a.a":"b"}, 42}`~~ | ~~Two Resources: `{job="svc_a", instance="a1", service.name="svc_a", service.instance.id="a1", region="eu", server.address="a1", server.port="", url.scheme="http"}` and the `svc_b`/`b1`/`us` twin. `target_info` supplies no covered attributes, so `service.*` are still MAY-defaulted from the pair — the same values the pair already holds.~~ | ~~Two Resources: `{prometheus.job="svc_a", prometheus.instance="a1", service.name="svc_a", service.instance.id="a1", region="eu", server.address="a1", server.port="", url.scheme="http"}` and the `svc_b`/`b1`/`us` twin.~~ | **~~Every Resource on this path is an *undeclared* target~~** ~~— the exposition carries no covered attributes at all, so C's declared-first precedence never engages here. Default derivation: identical to B, attribute for attribute. Never-derive opted in: `{prometheus.job="svc_a", prometheus.instance="a1", region="eu", server.address="a1", server.port="", url.scheme="http"}` and the `svc_b`/`b1`/`us` twin — no `service.*` at all, the pair is the identity.~~ |
| **~~4\. PRW exporter → series~~** | ~~`foo_total{job="svc_a", instance="a1", a_a="b", otel_scope_name="obs", otel_scope_version="v1.0.0"} 42` `target_info{job="svc_a", instance="a1", region="eu", server_address="a1", url_scheme="http"} 1` (plus the `svc_b` twin; `server_port=""` is dropped because `createAttributes` skips empty values)~~ | ~~Pair-first, and the pair holds the same values the derivation would have produced: `foo_total{job="svc_a", instance="a1", a_a="b", otel_scope_name="obs", otel_scope_version="v1.0.0"} 42` — byte-identical to Today. `target_info{job="svc_a", instance="a1", service_name="svc_a", service_instance_id="a1", region="eu", server_address="a1", url_scheme="http"} 1` — two labels more than Today, both exact duplicates of `job`/`instance` (note (i) of the `NoTranslation` scenario).~~ | ~~Byte-identical to A, both lines.~~ | ~~Default derivation — `service.name` is present, so it governs: `foo_total{job="svc_a", instance="a1", a_a="b", otel_scope_name="obs", otel_scope_version="v1.0.0"} 42` — byte-identical to Today. `target_info{job="svc_a", instance="a1", prometheus_job="svc_a", prometheus_instance="a1", region="eu", server_address="a1", url_scheme="http"} 1` — again two duplicate labels, under the pair's names this time. Never-derive — no declared identity, so the fallback consumes the pair and does not re-emit it: `foo_total{job="svc_a", instance="a1", a_a="b", otel_scope_name="obs", otel_scope_version="v1.0.0"} 42` `target_info{job="svc_a", instance="a1", region="eu", server_address="a1", url_scheme="http"} 1` Both lines byte-identical to Today.~~ |
| **~~Net effect~~** | ~~The path works. Identity survives the federate hop because `job`/`instance` were themselves derived from `service.*` and are derived back the same way.~~ | ~~Identical Prometheus output; `job`/`instance` gain an explicit OTLP home, and `target_info` grows two redundant labels.~~ | ~~Same as A.~~ | ~~Identical Prometheus output. With never-derive, byte-identical including `target_info` — the cleanest of the four, because the pair is recognized as the identity rather than laundered through `service.*`.~~ |

&nbsp;

**~~Notes~~**

&nbsp;

* **~~(q) All four columns produce the same Prometheus series here.~~** ~~This is the "they all agree" case. Every difference is confined to the OTLP middle — which attributes exist on the Resource, and therefore Resource and stream identity. For A, B and C-with-default-derivation that is the *additive breaking* change from case P1: no value changes, but every Resource gains attributes, so consumers keying on the exact attribute set see new streams across the upgrade. C with never-derive is the only column that adds the pair without also carrying a derived duplicate.~~  
* **~~(r) `job`/`instance` and the pair are the same values, so the pair looks redundant here.~~** ~~That is expected and is the point: on this path the pair is pure provenance confirmation. It only earns its keep when the exposed `job`/`instance` are *not* derivable from `service.*` — that is, when the origin was itself a Prometheus scrape rather than OTLP-native.~~  
* **~~(s) The one real loss on this path, which no option fixes: `service.namespace`, and declared identity generally.~~** ~~`extractJob` folds namespace into `job` as `<service.namespace>/<service.name>`, and `addTargetInfoMetric` then strips all three covered attributes from `target_info`. So an origin Resource with `service.namespace="ns"` re-enters OTLP as `service.name="ns/svc_a"` with no `service.namespace`, and there is no configuration in contrib to preserve the originals — the pull exporter has no `keep_identifying_resource_attributes`. This is the doc's R4 `keep_identifying=false` fork made concrete, and it is why C's declared-first rule is inert on the federation path: the declaration is destroyed one hop upstream, before C ever sees it. Fixing this needs the pull exporter to retain the covered attributes on `target_info`, independently of A/B/C.~~  
* **~~(t) The metric name gains `_total` permanently.~~** ~~`foo` (OTLP Sum) → `foo_total` on the wire → `foo_total` as the OTLP metric name after re-scrape → `foo_total` again on export. The suffix is idempotent (`trimSuffixAndDelimiter` then re-append), so it does not accumulate, but the OTLP name is now Prometheus-shaped unless the receiver sets `trim_metric_suffixes: true`. Unlike the `NoTranslation` scenario, the counter type survives intact throughout, because the family name ends in `_total` and OpenMetrics can therefore type it as a counter — the naming point from the `NoTranslation` discussion, arrived at automatically here.~~  
* **~~(u) `server.address`/`server.port` are noise on this path.~~** ~~The receiver splits `instance` as `host:port`; `a1` has no port, so it yields `server.address="a1"`, `server.port=""`. Harmless (the empty value is dropped on export) but it illustrates that the scrape-target enrichment convention assumes `instance` is an address, which is not true for a federated `instance` that came from a `service.instance.id`.~~  
* **~~(v) Verification.~~** ~~Step 2 is verbatim from contrib's `prometheusexporter` (real accumulator and collector, encoded as OpenMetrics 1.0). Step 3 is verbatim from contrib's `prometheusreceiver` transaction driven with the honored label sets — two `ResourceMetrics`, attributes and datapoints exactly as shown. Step 4 and the option columns are derived from `createAttributes`/`addResourceTargetInfo` and the proposal texts.~~

&nbsp;