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

Option C stores Prometheus scrape identity on the OTel Resource as **descriptive provenance** — the reserved attributes `prometheus.job` and `prometheus.instance` — while **respecting each Resource's own identifying attributes**. A Resource that declares service identity keeps it: The declaration is relayed as identity in both directions — a change from today's receiver behavior, where the job-derived value can displace the declaration depending on the exposition's escaping. The pair supplies identity only as a *fallback*, for targets that declare nothing, replacing today's choice between jobless output and polluting `service.name` with scrape-config strings. An opt-in never-derive setting stops synthesizing `service.*` from `job` and `instance`; until opted in, today's derivation is unchanged.

&nbsp;

Relative to the Proposed Design above, Option C is the Core Rules with three amendments:

&nbsp;

- ***Never-derive becomes an opt-in setting*****: A producer option stops synthesizing `service.name`, `service.namespace`, and `service.instance.id` from `job`/`instance`; the fallback keeps such targets from going jobless. The default later flips through the collector's own compatibility process (feature-gate graduation) — no Prometheus release gates it. Until opted in, the Core Rules' MAY-default derivation and its toggle are unchanged.**  
- **Inverted lookup order**: Consumers derive identity from the declared `service.*` subset first and fall back to the stored pair — the reverse of the Core Rules' pair-first lookup — so the OTel Resource's own identity always wins where it exists.  
- **Namespaced, descriptive storage**: `prometheus.job`/`prometheus.instance` rather than bare `job`/`instance`, carrying provenance in the name (the objection on which bare-name spec PR 4956 was not accepted), and stored as metadata rather than authority. Section 2's OTLP-endpoint `honor_labels` flag has no role here: nothing ever overrides a declared identity.

## Core Contract

Unless overridden here, the existing [Prometheus–OpenMetrics compatibility rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/) and the underlying exposition, OpenMetrics, Remote Write, and OTLP specifications apply.

&nbsp;

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

&nbsp;

Identity sources rank, highest first: Entity-declared identifying attributes; the declared default subset; the reserved pair, as fallback only. The pair never outranks a declared identity, and values from different identity sources are never combined.

&nbsp;

Option C preserves, per Resource and per translation unit:

* The normalized pair, exactly, as descriptive provenance — stored as the reserved pair on Prometheus → OTLP and emitted on generated `target_info` on OTLP → Prometheus — and, for undeclared Resources on entity-less paths, verbatim as the output `job` and `instance` labels via the fallback;  
* The covered attributes obtained from valid associated `target_info`, with exact presence and values, under agreement across same-pair contributors, with the covered names recognized per Covered Label Mapping — never dropped in favor of, or overwritten by, scrape identity.

&nbsp;

It does not preserve the source `target_info` series itself: sample cadence, HELP, UNIT, start timestamps, and exemplars are not represented. Sample timestamps and stale markers are used only to determine which target-metadata series are active. Receiver-added enrichment, external labels, explicitly promoted reserved attributes, and semantics-changing processors are outside the contract.

&nbsp;

Producer emission is a configuration opt-in and defaults to disabled (see Rollout). Consumers need no new behavior for Resources with a declared identity; the fallback is the only consumer addition, it can never override a declaration, and implementations MAY gate it, although it only changes a case that is degenerate today. Same-named data point attributes and metadata labels remain ordinary labels and never form a pair.

## Covered Label Mapping

Covered labels are read in two steps \- decode the wire encoding, then recognize the covered names:

&nbsp;

* On `target_info` series, once the wire encoding is decoded, both the dotted covered names and their three underscore forms \- `service_name`, `service_namespace`, and `service_instance_id` \- are recognized, under every profile. Flattening may have happened upstream of exposition, in a producer that sanitizes attribute names at record time, and no escaping negotiation reveals that. This is a bounded exception for three registered names on which identity depends, not a general un-escaping rule: no non-covered name is ever recovered from an underscore form. If several recognized forms of one covered name occur with the same value, they collapse to one covered attribute; if their values differ, that covered attribute is omitted with a bounded diagnostic. Recognized forms are consumed rather than retained as unrelated Resource attributes.&nbsp;&nbsp;  
* Recognition applies only where Option C emission is enabled, so default translation is unaltered: The compatibility specification's default is that label keys MUST NOT be altered, so this recognition belongs to the opt-in producer behavior to be specified — but some form of it is required by Section 1's own rule that the covered attributes from `target_info` are never dropped, which under escaped exposition has no dotted names to preserve without it. Where the wire carries a bare `service_name`, it rests on an assumption the wire cannot settle: That label is indistinguishable from an attribute literally so named, and no encoding reveals whether the producer flattened it at record time — `dots` and `values` settle only whether the exposition escaped it (see Pros and Cons for the cost).&nbsp;&nbsp;  
* A mapping profile additionally governs the escaping schemes that encode dots recoverably. Pull paths use the negotiated Prometheus escaping scheme: `allow-utf-8` carries the dotted names directly, and `dots` and `values` have unambiguous encodings for the three covered names (under `dots`, `service.name` travels as `service_dot_name` while an attribute literally named `service_name` travels as `service__name`, since `dots` doubles underscores; under `values` the dotted name travels as `U__service_2e_name` and the legacy-valid one is not escaped at all — and each decodes to a form recognized as the covered `service.name`, the encoding's distinction being deliberately discarded per the assumption above). `underscores` needs no decoding step — its forms are recognized directly. Remote Write has no negotiation; its receiver-side profile defaults to `exact` (decode nothing) and must be set to `dots` or `values` when the upstream producer uses those encodings, or the covered attributes stay encoded, the Resource reads as undeclared, and with never-derive in effect its identity silently becomes the scrape pair.&nbsp;&nbsp;  
* Ordinary series labels are never decoded — only `target_info` supplies covered attributes — and underscore-looking labels that are not one of the three forms remain ordinary metadata, except reserved-pair-looking labels, which are removed rather than recovered (see Target metadata association).

&nbsp;

Prometheus → OTLP decodes the selected profile, then recognizes covered names, before merging contributors. OTLP → Prometheus applies the output encoding after merging raw Resource attributes. Covered output names take precedence: a non-covered attribute that translates to the same label name is omitted with a bounded diagnostic and never overwrites or concatenates with the covered value, in place of today's joining of colliding values — reachable only where both names are emitted, under escaped output, since UTF-8-preserving output cannot collide. No profile claims general reversibility for arbitrary attribute names.

## Prometheus to OTLP

The producer finalizes labels under existing scrape rules (relabeling, `honor_labels` conflict handling, target filling, and label validation), groups ordinary points by the exact normalized pair, and associates `target_info`. The pair is stored once per Resource as the reserved attributes; `job` and `instance` are not repeated as point attributes. Identity assignment then follows the declaration:

* **Declared target** — valid covered attributes obtained from associated `target_info`: They are the Resource's declared identity, exactly as an SDK would have declared them; the pair is descriptive.  
* **Undeclared target** — no valid covered attributes: With never-derive opted in, the pair is the Resource's identity (see Entity Data Model for the entity-era form) and covered attributes stay absent; with derivation on (the default), covered attributes are derived as today and the Resource translates as declared-shaped, the fallback dormant.&nbsp;

&nbsp;

| Scenario | Behavior |
| :---- | :---- |
| Complete pair; no target metadata | Store the reserved pair; with never-derive opted in, leave covered attributes absent (identity fallback); otherwise derive them as today |
| Complete pair; valid, agreeing active `target_info` | Store the reserved pair descriptively; the merged covered attributes are the declared identity; consume the source series |
| Service-looking ordinary label | Keep as an ordinary point attribute; only `target_info` supplies covered attributes |
| `target_info` labels named `prometheus.job`/`prometheus.instance`, or their underscore forms | Drop them; the pair is taken from the scrape, never read from target metadata, so they cannot overwrite it~~Ignore as metadata; they cannot overwrite the reserved pair~~ |
| Identity incomplete after target filling | Fail that series with one bounded diagnostic; emit no partial pair |
| Invalid or conflicting `target_info` | Exclude the invalid series or conflicting key with one bounded diagnostic; valid siblings continue |
| `target_info` whose pair matches no ordinary series in the unit | Consume it without output; a stateful push producer may retain its accepted state for a later request |
| Producer emission disabled | Unchanged legacy translation; no reserved pair emitted |

### Target metadata association

Classification uses the final relabeled name. A series named exactly `target_info`, with scalar samples and Gauge, Info, unknown, or no type — for Remote Write 2.0, with Gauge, Info, or unset metadata — is usable target metadata. Any other type or a histogram shape is invalid target metadata. Suffix-looking names such as `target_info_total` stay ordinary metrics, and type suffixes are never stripped.

&nbsp;

Within one translation unit:

* Identify each source series by its complete final label set. Select its greatest-timestamp sample. Equal greatest timestamps are valid only when all selected samples are stale or all are non-stale with value `1`; otherwise that series is invalid. A stale selected sample is inactive, and a non-stale value other than `1` is invalid.  
* Determine all target-metadata state changes before associating ordinary series, so request order cannot change the result. Association is a snapshot operation, not a point-by-point temporal join.  
* Remove the name, identity labels, and reserved-pair-looking metadata labels; decode the remaining labels per Covered Label Mapping — the three underscore forms regardless of profile, encoded dots per the selected profile.&nbsp;  
* For a covered key, retain it only if every active contributor supplies the same non-empty string value, or every contributor omits it. A presence, type, empty-value, or value disagreement omits that key.  
* For other metadata, retain a final Resource key only if every active contributor supplies the same value. Presence, value, type, or translated-name disagreement omits that key. Unambiguous keys continue.

&nbsp;

Scrape association never crosses translation units. A push producer that carries association across requests MUST key its state by the exact normalized pair — a hash may index the state but cannot replace exact equality — scoped per receiver instance and, where applicable, tenant. Within a pair it retains the newest accepted state per complete `target_info` label set: a newer value-`1` sample replaces the stored metadata, a newer stale marker retires it, and older samples never resurrect retired metadata. A valid target-info-only request may commit state. State is bounded; eviction, overflow, or restart invalidates the whole pair entry, and cross-request preservation applies only while the entry is retained.

&nbsp;

If a changed label set is not accompanied by a stale marker for the old series, both remain active. Their metadata is merged under the agreement rules above; the translator does not silently treat the new series as a per-key replacement. Remote Write delivery, partial-write accounting, and cross-request atomicity remain governed by the protocol and receiver.

## OTLP to Prometheus

Resources with a declared identity translate under **unchanged legacy translation**: `job` and `instance` derive from the declared subset (or, for entity-bearing payloads, from entity-identity synthesis), `keep_identifying_resource_attributes` retains its exact meaning, and the reserved pair — an ordinary descriptive attribute — appears on generated `target_info` under the output mapping profile (`prometheus_job`, `prometheus_instance`). No new consumer behavior exists for this class.

&nbsp;

| Scenario | Behavior |
| :---- | :---- |
| Declared identity present, with or without a reserved pair | Unchanged legacy translation; the pair is ordinary descriptive metadata on generated `target_info` |
| No declared identity; valid reserved pair | Fallback: use the pair verbatim as the `job` and `instance` labels; the consumed pair is not additionally emitted as `target_info` metadata |
| No declared identity; one reserved attribute present, or either value empty or non-string | Today's service-less handling with one bounded diagnostic; never mix reserved and derived values; handle the invalid reserved attributes as ordinary Resource attributes |
| Point attributes named `prometheus.job` or `prometheus.instance` | Ordinary translated labels; the fallback never reads them |
| Reserved attribute explicitly promoted (`promote_resource_attributes`, or `promote_all_resource_attributes` minus `ignore_resource_attributes`) | Emit it under its translated name on ordinary series; identity handling is unchanged |
| Same-pair fan-in among fallback Resources in one unit | Emit at most one generated `target_info` for the pair: covered keys are absent by definition; other attributes merge by agreement, disagreements omitted with a bounded diagnostic; samples follow the consumer's existing `target_info` scheduling |
| `target_info` generation disabled or renamed | The setting remains authoritative |

&nbsp;

Fan-in among declared-identity Resources follows existing behavior unchanged — their identity, and therefore their `target_info` grouping, is exactly what it is today.

&nbsp;

Output rules:

* Generated `target_info` follows existing conventions — a value-`1` `target_info` Gauge, or OpenMetrics `target` Info where that representation is preserved — never both. Sample scheduling is unchanged: ingestion interpolation, Remote Write timestamp selection, and timestamp-less pull exposition keep existing behavior.  
* Collisions with a real metric named `target_info` follow existing behavior. PromQL matches the concrete `target_info` name, not the OpenMetrics family name `target`.  
* Exact round-tripping of non-covered dotted attribute names requires a UTF-8-preserving translation strategy; the covered names survive underscore exposition through recognition.

## Entity Data Model

The OpenTelemetry Entity data model (in development) restructures Resource identity: when a payload carries entities, the identifying resource attribute set is exactly the union of the entities' identifying attributes and the draft Prometheus entity-ingestion rules synthesize the `instance` label from that set as a UUIDv5, with no verbatim carve-out available — `job` and `instance` must key the full identifying set for the `target_info` join to hold. Option C composes with those rules as the general case and requests no synthesis carve-outs:

* **Declared targets relay their declared identity**: The recommended producer policy is to declare the covered attributes as the `service.instance` entity's identifying attributes — the entity-era encoding of the same default-subset convention consumers apply, and the condition under which a scraped application and the same application pushing OTLP directly share one synthesized identity. A producer MAY instead declare no entities; the consumer's entity-less default then still yields declared-identity semantics via legacy label derivation, at the cost of identity convergence with entity-bearing native traffic. Exposition-carried entity structure, once a mechanism for relaying it exists, is relayed rather than reconstructed.  
* **Undeclared targets carry the scrape-target entity**: With never-derive in effect, a scraped target that exposes no identifying resource attributes carries the `prometheus.scrape_target` entity (working name) whose identifying attributes are the reserved pair — the entity-era form of the fallback, and the sole identifying entity on such Resources. Under the default derivation, such targets translate as declared-shaped, and the producer declares no entities for them — the entity-less default preserves today's translation until never-derive is opted in.  
* **The pair's role follows the declaration**: On declared Resources the reserved pair is descriptive and rides generated `target_info`; on undeclared Resources it is the identifying set, and its `target_info` visibility follows the identifying-attribute partition rather than descriptive handling. Receiver-added enrichment stays descriptive, since marking additional entities as identifying changes series identity for every consumer under any synthesis.  
* Byte-exact `job`/`instance` output labels are therefore an entity-less, undeclared-target property; for everything else, identity follows the declaration or the synthesis, and the original scrape coordinates remain queryable through the `target_info` join.

&nbsp;

In the entity era, identity policy therefore reduces to which entities a producer declares: Consumers simply honor entity-declared identity and need no pair-specific rules. The discipline above — declare the Resource's own identity where it exists, the scrape-target entity otherwise — is the recommended default. A deployment that prefers scrape-identity semantics even for declared targets can express that policy by declaring the scrape-target entity as the identifying entity and keeping the covered attributes descriptive, with no consumer changes; labels still synthesize from the identifying set, so this buys per-target distinctness and target-aligned lifecycle, not byte-exact labels.

## Round-Trip Use Cases

Concrete traces through the rules above, each naming its configuration. Cases assume underscores escaping unless noted; where stated, Option C's outcome is escaping-independent. Receiver-added enrichment (`server.address`, `server.port`, `url.scheme`) is omitted from the traces: it is unchanged by Option C and rides generated `target_info` as today.

### R1 — Declared target: Prometheus → OTLP → Prometheus (emission on)

An OTel SDK application behind the SDK's Prometheus exporter exposes `target_info{service_name="my_service", service_instance_id="my_instance_id"} 1`, scraped as `job="my_job"`, `instance="my_instance"`.

&nbsp;

* Producer output: `Resource{prometheus.job="my_job", prometheus.instance="my_instance", service.name="my_service", service.instance.id="my_instance_id"}` — the declaration is relayed as identity, because Option C recognizes the `service_name` and `service_instance_id` forms on `target_info` (today they land as stray attributes while `service.name` holds the job). The pair is descriptive. The Resource is the same whether the exposition negotiated `allow-utf-8` or `underscores`, and whether the flattening happened in the exposition or in the exporter beforehand — outcomes today diverge on both.  
* Consumer output, `keep_identifying_resource_attributes=false`: `foo{job="my_service", instance="my_instance_id", A="B"}` and `target_info{job="my_service", instance="my_instance_id", prometheus_job="my_job", prometheus_instance="my_instance"} 1`.  
* Consumer output, `keep_identifying_resource_attributes=true`: `foo{job="my_service", instance="my_instance_id", A="B"}` and `target_info{job="my_service", instance="my_instance_id", service_name="my_service", service_instance_id="my_instance_id", prometheus_job="my_job", prometheus_instance="my_instance"} 1`.  
* Outcome: Declared identity governs end-to-end; the scrape coordinates are one `target_info` join away; the output `job`/`instance` differ from the original scrape labels — the deliberate declared-target shift (see Pros and Cons). Relative to today, generated `target_info` changes once at adoption: its identity labels shift with the declared-target shift, the pair labels appear, and today's stray `service_name`/`service_instance_id` labels are consumed into the covered attributes — present again only with `keep_identifying_resource_attributes`.

### R2 — Undeclared target, never-derive opted in: Prometheus → OTLP → Prometheus

node\_exporter scraped as `job="node"`, `instance="10.0.0.5:9100"`; no `target_info`.

&nbsp;

* Producer output: `Resource{prometheus.job="node", prometheus.instance="10.0.0.5:9100"}` — no `service.*`.  
* Consumer output (fallback): `node_cpu_seconds_total{job="node", instance="10.0.0.5:9100", cpu="0", mode="idle"}` — byte-exact. The consumed pair is not additionally emitted as `target_info` metadata, so with enrichment omitted no `target_info` appears here; in practice the omitted `server.*` attributes generate `target_info{job="node", instance="10.0.0.5:9100", server_address="10.0.0.5", server_port="9100"} 1` — still without the pair labels.  
* Outcome: Byte-exact round trip — the entity-less, undeclared-target property.

### R3 — Undeclared target, default derivation: Prometheus → OTLP → Prometheus

Same scrape as R2, never-derive not opted in.

&nbsp;

* Producer output: `Resource{prometheus.job="node", prometheus.instance="10.0.0.5:9100", service.name="node", service.instance.id="10.0.0.5:9100"}` — derived as today; declared-shaped, the fallback dormant.  
* Consumer output, `keep_identifying_resource_attributes=false`: `node_cpu_seconds_total{job="node", instance="10.0.0.5:9100", cpu="0", mode="idle"}` and `target_info{job="node", instance="10.0.0.5:9100", prometheus_job="node", prometheus_instance="10.0.0.5:9100"} 1` — legacy derivation from the derived `service.*`; the pair rides `target_info` descriptively. With `=true`, `service_name="node"` and `service_instance_id="10.0.0.5:9100"` additionally appear on `target_info`.  
* Outcome: Ordinary-series labels identical to today; generated `target_info` gains the two pair labels (the target already produces one, via receiver-added attributes such as `server.address`) — a one-time label-set change for that series at adoption; otherwise purely additive. With `=true`, the exposed `service_name`/`service_instance_id` labels make this target read as declared on a downstream scrape — derived values become a declaration, indistinguishable from an application's own.

### R4 — OTLP-native origin: OTLP → Prometheus → OTLP (re-scrape with `honor_labels: true`, emission on)

Origin: `Resource{service.name="my_service", service.instance.id="my_instance_id", k8s.pod.name="p"}`, no pair.

&nbsp;

* First consumer output, `keep_identifying_resource_attributes=false`: `foo{job="my_service", instance="my_instance_id"}` and `target_info{job="my_service", instance="my_instance_id", k8s_pod_name="p"} 1`.  
* First consumer output, `keep_identifying_resource_attributes=true`: `foo{job="my_service", instance="my_instance_id"}` and `target_info{job="my_service", instance="my_instance_id", service_name="my_service", service_instance_id="my_instance_id", k8s_pod_name="p"} 1`.  
* Re-scrape producer: the honored labels form the pair `("my_service", "my_instance_id")`. Three forks:  
  * From the `keep_identifying=true` exposition: `Resource{prometheus.job="my_service", prometheus.instance="my_instance_id", service.name="my_service", service.instance.id="my_instance_id", k8s_pod_name="p"}` — the `target_info` labels declare the covered attributes, so declared identity is restored with exact values. Note `k8s.pod.name` returns as `k8s_pod_name`: non-covered names are never un-escaped.  
  * From the `keep_identifying=false` exposition, default derivation: `Resource{prometheus.job="my_service", prometheus.instance="my_instance_id", service.name="my_service", service.instance.id="my_instance_id", k8s_pod_name="p"}` — the same attribute set as the fork above, byte for byte: the target is undeclared, so `service.*` are re-derived from the pair, and the values coincide with the originals (the labels were derived from them). The provenance difference — derivation, not declaration — is invisible in the flat Resource and only becomes observable in the entity era.  
  * From the `keep_identifying=false` exposition, never-derive: `Resource{prometheus.job="my_service", prometheus.instance="my_instance_id", k8s_pod_name="p"}` — `service.*` are absent; the declared identity is laundered into the pair (the fidelity gap Section 2's `keep_identifying` flip closes).  
* Outcome: Value-lossless with `keep_identifying=true`; provenance-lossy or attribute-lossy without it.

### R5 — Mixed versions: new producer, old consumer

* Declared target (R1's Resource) at an old consumer (`keep_identifying_resource_attributes=false` shown): `foo{job="my_service", instance="my_instance_id", A="B"}` and `target_info{job="my_service", instance="my_instance_id", prometheus_job="my_job", prometheus_instance="my_instance"} 1` — identical to R1's corresponding fork: declared-identity handling is today's behavior, and the pair is ordinary metadata under existing rules. Safe immediately.  
* Undeclared, never-derive Resource (R2's) at an old consumer without fallback support: `node_cpu_seconds_total{cpu="0", mode="idle"}` — no `job` or `instance` labels at all — and no `target_info` (it is suppressed when no identity label is derivable). This is why fallback support deploys before never-derive is enabled (see Rollout).

### R6 — Entity era (draft rules; see Entity Data Model)

R1's, R2's, and R3's scrapes, replayed once the entity data model is in effect at producer and consumer (emission on). Producers follow the recommended entity policy — which declares no entities for targets whose `service.*` they derived — and consumers synthesize identity labels from entity-declared identifying attributes; the draft specifies `instance` as a UUIDv5 of the identifying set. Angle-bracketed values below are symbolic, standing in for the draft's two unresolved rules: the `job` synthesis rule, and where identifying attributes surface on `target_info`.

&nbsp;

**Declared target, recommended policy** — R1's scrape: the application exposes `foo{A="B"}` and `target_info{service_name="my_service", service_instance_id="my_instance_id"} 1`, scraped as `job="my_job"`, `instance="my_instance"`.

&nbsp;

* Producer output: `Resource{prometheus.job="my_job", prometheus.instance="my_instance", service.name="my_service", service.instance.id="my_instance_id"}` — its attribute set byte-identical to R1's Resource — now carrying the entity declaration `{type: service.instance, id_keys: [service.name, service.instance.id]}`.  
* Consumer output: `foo{job="<per the draft's job rule>", instance="<UUIDv5 of {service.name="my_service", service.instance.id="my_instance_id"}>", A="B"}` and `target_info{job="<per the draft's job rule>", instance="<the same UUIDv5>", prometheus_job="my_job", prometheus_instance="my_instance"} 1` — identity labels are synthesized from the entity-declared identifying set rather than copied from `service.*` values (R1 gave `job="my_service"`, `instance="my_instance_id"`); the pair rides `target_info` descriptively exactly as in R1. Whether `service_name`/`service_instance_id` additionally appear on `target_info` follows the draft's identifying-attribute placement (the entity-era analogue of `keep_identifying_resource_attributes`), the second open point above.  
* Outcome: The same application pushing OTLP directly declares the same entity and synthesizes the same `job` and `instance` — scraped and pushed series converge on one identity (path independence). Relative to R1, the identity labels change again at entity adoption — an effect of the draft's synthesis common to every option, not an Option C rule.

&nbsp;

**Undeclared target, never-derive opted in** — R2's scrape: node\_exporter exposes `node_cpu_seconds_total{cpu="0", mode="idle"}` and no `target_info`, scraped as `job="node"`, `instance="10.0.0.5:9100"`.

&nbsp;

* Producer output: `Resource{prometheus.job="node", prometheus.instance="10.0.0.5:9100"}` — no `service.*`, as in R2 — now carrying the entity declaration `{type: prometheus.scrape_target, id_keys: [prometheus.job, prometheus.instance]}`.  
* Consumer output: `node_cpu_seconds_total{job="<per the draft's job rule>", instance="<UUIDv5 of {prometheus.job="node", prometheus.instance="10.0.0.5:9100"}>", cpu="0", mode="idle"}` — the same synthesis rule applied to the scrape-target entity's set, with no verbatim carve-out: the entity supersedes the entity-less fallback that R2 exercised, so the pair is synthesis input rather than copied verbatim. Its original strings surface wherever the draft places identifying attributes (the second open point), rather than under R1's descriptive `target_info` handling; in R2 the pair was itself the identity labels and needed no separate surfacing.  
* Outcome: R2's byte-exact round trip does not survive entity declaration: identity is stable per target but synthesized; the original coordinates stay queryable where the draft surfaces identifying attributes (the second open point above). Byte-exact `job`/`instance` output is an entity-less, undeclared-target property (Entity Data Model, last bullet).

&nbsp;

**Undeclared target, default derivation** — R3's scrape, never-derive not opted in: under default derivation the producer declares no entities (see Entity Data Model).

&nbsp;

* Producer output: `Resource{prometheus.job="node", prometheus.instance="10.0.0.5:9100", service.name="node", service.instance.id="10.0.0.5:9100"}` with no entity declaration — byte-identical to R3's Resource.  
* Consumer output, `keep_identifying_resource_attributes=false`: `node_cpu_seconds_total{job="node", instance="10.0.0.5:9100", cpu="0", mode="idle"}` and `target_info{job="node", instance="10.0.0.5:9100", prometheus_job="node", prometheus_instance="10.0.0.5:9100"} 1` — byte-identical to R3: with no entities on the payload, the consumer's entity-less default applies and legacy label derivation proceeds unchanged. With `=true`, `service_name="node"` and `service_instance_id="10.0.0.5:9100"` additionally appear on `target_info`, as in R3.  
* Outcome: The entity era changes nothing for this class until never-derive is opted in.

&nbsp;

At an entity-unaware consumer, both entity-bearing Resources above degrade to their flat-era handling: the declared target to R1's outputs via legacy derivation from the covered attributes, the undeclared target to R2's verbatim fallback — or, without fallback support, to R5's suppression. The entity's identifying attributes are ordinary resource attributes to a consumer that predates entities.

&nbsp;

Under Options A and B, the first scenario's declared-target payload must instead key its identity byte-exactly on the stored pair — ignoring the entity-declared identifying set — or claim a verbatim carve-out from the synthesis; that is the incompatibility recorded in the comparison table's entity row.

## Non-goals

- Identity-precedence configuration: No option exists to make the reserved pair outrank a declared identity.  
- Byte-exact `job`/`instance` label round-trips for declared-identity Resources or entity-bearing payloads: Identity follows the declaration or the entity synthesis.  
- Partitioning of colliding declarations: Resources that declare the same identity merge exactly as they do on the native OTLP path, and the pair does not split them.  
- Cross-request or cross-output-unit atomicity, batch envelopes, delivery, deduplication, or exactly-once semantics, and protocol response or accounting changes.  
- Preservation of source `target_info` sample timing beyond using timestamps and staleness for association: staleness and series lifecycle follow existing protocol rules.

## Requirements Mapping

- **Separate Storage**: Satisfied by construction — the reserved pair and covered attributes are distinct Resource attributes and never overwrite each other; the pair is provenance, the covered attributes are identity where declared.  
- **Universal Join Key**: Declared Resources derive `job`/`instance` exactly as today; undeclared Resources gain them through the fallback — strictly better than today, where never-derive alone would leave them jobless.  
- **Queryable Resource Attributes**: With never-derive in effect, Option C never writes scrape-config strings into covered attributes, and they are never dropped in favor of scrape identity; values that a deriving upstream hop already wrote to `target_info` are relayed as declarations, since nothing on the wire distinguishes them from an application's own (see R4); their visibility on `target_info` continues to follow `keep_identifying_resource_attributes` and Section 2's planned default flip.&nbsp;  
- **Non-Breaking Server Compatibility**: Structural — consumer behavior is bit-identical for all existing traffic at default configuration, because declared-identity handling is untouched, the pair is ordinary metadata under existing rules, and the fallback activates only for a payload class that is empty today and degenerate if it existed. One non-default case changes: Where a Resource carries a covered attribute and a same-named non-covered attribute and both are emitted — which needs `keep_identifying_resource_attributes=true` or explicit promotion, plus escaped output — the covered value is emitted alone with a diagnostic instead of the two being joined. Option C's own producers never emit such a Resource; today's escaped scrapes do. This still exceeds the requirement, which only asks that breaks wait for a major version; Option C queues none.

&nbsp;

One consequence is deliberate: With never-derive in effect, an undeclared target yields a Resource with **no `service.*` at all**. The fallback supplies its output `job`/`instance`, but generic OTel consumers group such Resources as service-less rather than under a scrape-config-derived name — per Practical Issue 3, an absent service identity is preferable to a polluted one. This requires the compatibility specification to repeal, for Option C paths, its current rule that `service.name` and `service.instance.id` MUST be filled on scrape.

&nbsp;

Operators who prefer job-derived service names can still create them deliberately — e.g. an OTTL statement such as `set(resource.attributes["service.name"], resource.attributes["prometheus.job"])` — turning the derivation into an explicit per-pipeline choice rather than a default; such a processor is semantics-changing and intentionally outside the contract.

## Pros and Cons

Pros:

* **Structural backwards compatibility**: Consumer behavior is bit-identical for all existing traffic with no configuration change, gates, or major-version flag day. Prometheus's compatibility policy — breaking changes only in major versions — is not merely respected but never drawn upon: No break is needed now or queued for later. The fallback changes only a payload class that is empty today and degenerate if it existed, and the only consumer-visible change is the collision rule noted under Requirements Mapping, which no default configuration reaches.  
* **Declared identity is always respected**: A Resource's own identifying attributes govern translation, so a scraped application and the same application pushing OTLP directly share one identity — identity is path-independent.  
* **The stated pains are solved**: With never-derive in effect, no producer writes scrape-config strings into `service.name` — per producer today, by default at the major-version flip; values an upstream deriving hop already exposed are still relayed — neither identity is dropped in favor of the other, and undeclared targets gain honest `job`/`instance` join keys instead of jobless output or fabricated service names.&nbsp;  
* **Provenance-safe names**: `prometheus.job`/`prometheus.instance` state their origin, so a consumer never has to guess whether an attribute named `job` means scrape identity, and no `honor_labels`\-style disambiguation apparatus is needed.  
* **Minimal consumer surface**: Existing identity derivation is retained unchanged everywhere; each consumer adds one fallback conditional, and the entity-era composition requests no synthesis carve-outs.  
* **Scrape coordinates stay operable**: The original scrape config and target address are always visible — as the identity labels themselves on fallback Resources, and one `info`\-join away on `target_info` for declared ones.

&nbsp;

Cons:

* **No byte-exact `job`/`instance` round-trip for declared-identity Resources**: An application's series re-enter Prometheus under its declared (or entity-synthesized) identity, not the original scrape labels, so dashboards and rules keyed on those labels do not survive the OTLP hop. Today's receiver collision behavior — the job-derived value displacing the declaration under escaped exposition — is what restores scrape  labels server-side; Option C replaces that escaping-dependent coin flip with a deterministic rule, extending to escaped exposition what already happens under UTF-8.  
* **Undeclared targets are service-less on OTel-native backends**: An absent service identity is preferable to a polluted one, but with never-derive in effect their grouping regresses relative to defaulting; the explicit OTTL derivation is the mitigation.  
* **Colliding declarations merge**: Resources declaring the same identity collapse into one series identity, inheriting the push path's risk profile; the pair witnesses the collision on `target_info` but does not partition it.  
* **Producer-side machinery**: Profile selection, covered-name recovery with its collapse and conflict rules, target-metadata association, and pair-keyed grouping with cross-request state on push paths are all new producer work — the bulk of the design's implementation cost, and the part Variant C.1 trims.&nbsp;&nbsp;  
* **Generated `target_info` reshapes once at adoption**: The pair labels appear on it, so its series identity changes and the prior series goes stale — one event per target, visible to anything joining on `target_info` (R1, R3).&nbsp;&nbsp;  
* **Underscore forms on `target_info` are decoded on an assumption**: A label named `service_name` cannot be distinguished on the wire from an attribute literally named `service_name`, so a Resource attribute of that literal name is renamed and becomes identity after a Prometheus round trip. The exception is bounded to three registered names on `target_info`, its prevalence is unmeasured, as is Option A's converse risk; and it is what lets declarations survive producers that flatten names before exposition, which profile-gated recognition drops whenever the profile expects dotted names. Variant C.1 is Option C without this rule.&nbsp;&nbsp;  
* **Identity changes when a target's declaration status changes**: A target that starts (or stops) exposing identifying attributes via `target_info` flips between fallback and declared identity, breaking its series once — an event triggered by an application change the scrape operator may not control.  
* **Standardization is a prerequisite**: Reserved-name registration, the fallback semantics, the scrape-target entity type, and the MUST-fill repeal must all land before conforming implementations can ship.  
* **The namespaced prefix must be learned**: OTTL and processor work targets `prometheus.job`, not `job`.  
* **Covered-attribute round-trip fidelity is configuration-dependent**: Until Section 2's `keep_identifying_resource_attributes` default flip, a declared identity transiting Prometheus and re-scraped without its `target_info` metadata is laundered into the pair.

## Comparison with Options A and B

| Aspect | Option A (bare) | Option B (namespaced) | Option C |
| :---- | :---- | :---- | :---- |
| Resource attributes | `job`, `instance` | `prometheus.job`, `prometheus.instance` | Same as B |
| Role of the stored pair | Authoritative identity, looked up first; server-side once `honor_labels` is enabled | Authoritative identity at aggregated exporters (Section 1); at the Prometheus server, gated by Section 2's `honor_labels`, off by default today | Descriptive provenance; identity fallback for undeclared Resources only |
| Consumer activation | Requires the `honor_labels` server flag: Bare names are generic, unreservable attribute keys a consumer cannot distinguish from scrape identity — whether they already occur in OTLP traffic is unmeasured, but they remain open to collision permanently | No disambiguation flag needed, the name being self-describing, but Section 2's `honor_labels` still gates the server until its major-version flip | None for declared traffic — behavior is unchanged; the fallback MAY be gated |
| `service.*` defaulting from job/instance | Core Rules MAY-default plus toggle | Core Rules MAY-default plus toggle | MAY-default until the major-version flip; opt-in never-derive |
| Breaking risk | Several flows marked BREAKING in the tables above | Low today; the `honor_labels` flip shifts identity for pair-bearing payloads at a major version | None structurally; existing traffic translates bit-identically at default configuration, one non-default collision case aside (see Requirements Mapping) |
| Collector / OTTL UX | Natural label names | Prefix must be learned | Prefix must be learned |
| Semantic-convention registration | Arguably none needed | Needed | Needed, as reserved descriptive names plus fallback semantics |
| Entity data model compatibility | Pair-first, byte-exact semantics cannot survive entity-identity synthesis without a verbatim carve-out, and bare names are unsuitable as entity identifying attributes | Same pair-first conflict; the namespaced names could register as entity identifying attributes, but only by giving up byte-exactness | Composes with the general synthesis rules, no carve-outs requested (see Entity Data Model) |

&nbsp;

On the central difference — precedence — pair-first lookup does not eliminate identity overwriting; it inverts it: Observed scrape coordinates displace an application's declared identity, the mirror image of Practical Issue 1\. Declared-first is the only order under which no identity is ever overwritten — every Resource keeps whichever identity was asserted about it, and the pair fills the gap when none was.

&nbsp;

Variant C.1 below shares Option C's column above, save for the Breaking risk row: For targets whose covered names reach the producer flattened, it declines to read the declaration, which keeps their `job` and `instance` labels byte-exact but forfeits identity convergence with the same application pushing OTLP, and makes its own producers emit the colliding Resource that Option C's never emit.

### Option B stated as pros and cons

Option B, as this document specifies it, is the reserved pair under namespaced names with Section 1's pair-first lookup: `prometheus.job` and `prometheus.instance` are stored on the Resource and consulted before the declared `service.*` subset. It is therefore Option C's storage decision with the opposite precedence, and the pros and cons below are meant to state it at full strength rather than as a foil.

&nbsp;

Pros:

* **Byte-exact `job`/`instance` round-trips for every target**: Scrape coordinates come back verbatim whatever the target declares — at aggregated exporters today, and at the Prometheus server once Section 2's `honor_labels` default flips — so dashboards, rules, and alerts keyed on them survive the OTLP hop, the compatibility property Option C forgoes for declared targets and C.1 forgoes for dotted exposition.&nbsp;&nbsp;  
* **Provenance-safe names**: Shared with C — a consumer never has to guess whether an attribute means scrape identity, and no flag is needed to disambiguate what the attribute means, though Section 2's `honor_labels` still gates when the server honors it.&nbsp;&nbsp;  
* **No collision with existing OTLP traffic**: `prometheus.job` is a new reserved name, so no payload carries it today and no existing user's series identity changes — the axis on which Option A breaks.&nbsp;&nbsp;  
* **Least producer machinery**: Storage plus the existing derivation, with no covered-name recovery and no fallback semantics. The target-metadata association questions Option C specifies arise for B too, on push paths especially; B leaves them to existing implementation behaviour rather than answering them.

&nbsp;

Cons:

* **It elevates the scrape target's coordinates to the Resource's identity**: An application that declares `service.*` has that declaration stored and then ignored, so scraped identity overwrites declared identity — the mirror image of Practical Issue 1, and the objection Option C's declared-first order exists to answer.&nbsp;&nbsp;  
* **Identity is path-dependent**: The same application scraped and pushing OTLP directly lands under two identities, so its series do not merge; Option C's declared-first order is what makes the two paths converge.&nbsp;&nbsp;  
* **`service.name` pollution is unaddressed**: B keeps the Core Rules' MAY-default derivation with no never-derive setting, so scrape-config strings continue to occupy the semantic slot — Practical Issue 3 persists.&nbsp;&nbsp;  
* **The entity era closes both routes**: Pair-first, byte-exact semantics cannot survive entity-identity synthesis, and no verbatim carve-out is available, since `job` and `instance` must key the full identifying set. Registering the namespaced names as entity identifying attributes is the remaining route, and it yields identity that is stable per target but synthesized rather than byte-exact — surrendering B's central promise — while making a scraped application's identity its scrape target's permanently. B also states no producer entity policy, so each adopter would pick one.  
* **Its central promise waits on a flag flip**: Byte-exact round-tripping through Prometheus's own OTLP endpoint requires `honor_labels=true`, which Section 2 defers to a future major version. Option C's declared-first order instead matches the endpoint's present default, which is why Section 2's flag has no role in it — C's semantics arrive without a flag day, B's most valuable property does not.

&nbsp;

The options therefore separate into two independent decisions. Naming — bare or namespaced — is settled jointly by B, C, and C.1 against A. Given namespaced storage, what remains is precedence: pair-first (B) or declared-first (C, C.1), and then whether the producer recovers flattened covered names (C) or leaves them as metadata (C.1).

## Variant C.1: Without Covered-Name Recognition

Option C.1 is Option C with one rule removed: The producer does not recover the covered names from their underscore forms on `target_info`. Decoding stays — `dots` and `values` encodings are reversed as before, recoverably for the three covered names — so only the guess is dropped, and a decoded label named `service_name` remains an ordinary Resource attribute of that name. Everything else is identical: the reserved pair, never-derive, the identity fallback, the entity composition, and every consumer rule. Two things follow. C.1 confines to recoverable encodings its departure from the specification's default that label keys are not altered, inferring no covered name from a lossy form — though it still asks for the reserved-name registration, the fallback and never-derive semantics, the scrape-target entity type, and the MUST-fill repeal. And its normative delta exceeds a deleted recognition step: Section 1's rule that the covered attributes from `target_info` are never dropped has to be relaxed, because an exposed `service.name` that reaches the producer flattened is then not preserved under its dotted name.

&nbsp;

The difference appears wherever a covered name is in a bare underscore form once the wire encoding is decoded. An exporter that sanitizes attribute names at record time produces one, as does exposition negotiated at `underscores`; so does an attribute literally named `service_name`, which reaches that form under every profile — escaped and decoded again under `dots`, never escaped at all under `values`. Such a target has no covered attributes under C.1, so it is undeclared, and an attribute genuinely named `service_name` keeps its name instead of being reinterpreted. Where exposition carries the dotted names the two designs coincide, and targets that expose no `target_info` at all (R2, R3) are unaffected.

### Round-Trip Use Cases for C.1

&nbsp;

R1's scrape throughout, emission on: an OTel SDK application behind the SDK's Prometheus exporter exposes `foo{A="B"}`\` and `target_info{service_name="my_service", service_instance_id="my_instance_id"} 1`, scraped as `job="my_job"`, `instance="my_instance"`.

&nbsp;

**V1 — Flattened exposition, default derivation**

* Producer output: `Resource{prometheus.job="my_job", prometheus.instance="my_instance", service.name="my_job", service.instance.id="my_instance", service_name="my_service", service_instance_id="my_instance_id"}` — the exposed labels are kept verbatim and are not covered attributes, so the target is undeclared and `service.*` are derived from the pair as today. Both spellings sit on one Resource with different values: `service.name` holds the scrape config, `service_name` holds the application.  
* Consumer output, `keep_identifying_resource_attributes=false`: `foo{job="my_job", instance="my_instance", A="B"}` and `target_info{job="my_job", instance="my_instance", prometheus_job="my_job", prometheus_instance="my_instance", service_name="my_service", service_instance_id="my_instance_id"} 1`.  
* Consumer output, `keep_identifying_resource_attributes=true`\`: `foo{job="my_job", instance="my_instance", A="B"}`\` and `target_info{job="my_job", instance="my_instance", service_name="my_job", service_instance_id="my_instance", prometheus_job="my_job", prometheus_instance="my_instance"} 1` — the derived covered attributes translate to `service_name` and `service_instance_id`, the same label names the verbatim attributes already carry, so covered output names take precedence and the application's values are omitted with a bounded diagnostic. This is a regression against today rather than only against Option C: today's consumer joins colliding values, emitting `service_name="my_job;my_service"`, so `my_service` survives. It is also confined to escaped output — under a UTF-8-preserving output translation the two names never collide and both survive.  
* Outcome: Ordinary-series labels are byte-identical to today's scrape, which is the compatibility gain; with `keep_identifying_resource_attributes=false` the only delta versus today is the two pair labels added to generated `target_info`, a one-time label-set change at adoption. The application's own identifiers ride along on the Resource but are never read as identity, and under Section 2's planned `keep_identifying` default flip they are dropped from the output entirely.

&nbsp;

**V2 — Flattened exposition, never-derive opted in**

* Producer output: `Resource{prometheus.job="my_job", prometheus.instance="my_instance", service_name="my_service", service_instance_id="my_instance_id"}`\` — no `service.*` at all; the exposed labels remain ordinary attributes.  
* Consumer output (fallback): `foo{job="my_job", instance="my_instance", A="B"}` and `target_info{job="my_job", instance="my_instance", service_name="my_service", service_instance_id="my_instance_id"} 1` — the consumed pair is not additionally emitted, and with no covered attributes present no output name collides, so the application's values survive.  
* Outcome: The variant's best case — byte-exact `job`/`instance` and the application's own values preserved, but only as flattened metadata: A generic OTel consumer sees a service-less Resource and cannot group these series with the same application's traces.

&nbsp;

**V3 — Dotted exposition (`allow-utf-8`)**

The same application, exposing \`target\_info{"service.name"="my\_service", "service.instance.id"="my\_instance\_id"} 1\`.

&nbsp;

* Producer output: `Resource{prometheus.job="my_job", prometheus.instance="my_instance", service.name="my_service", service.instance.id="my_instance_id"}` — identical to R1 under Option C: the dotted names need no recovery.  
* Consumer output, `keep_identifying_resource_attributes=false`: `foo{job="my_service", instance="my_instance_id", A="B"}` and `target_info{job="my_service", instance="my_instance_id", prometheus_job="my_job", prometheus_instance="my_instance"} 1` — identical to R1.  
* Outcome: C.1 and C coincide here, costs included: this target takes the declared-target shift and its output labels stop matching the scrape. Which case a deployment lands in is therefore decided by its exporter's naming rather than by its configuration.

&nbsp;

**V4 — Entity era**

* Flattened exposition, never-derive: V2's Resource carries `{type: prometheus.scrape_target, id_keys: [prometheus.job, prometheus.instance]}`, so the consumer emits `job="<per the draft's job rule>"` and `instance="<UUIDv5 of {prometheus.job="my_job", prometheus.instance="my_instance"}>"`. The same application pushing OTLP directly declares `{type: service.instance, id_keys: [service.name, service.instance.id]}` and synthesizes from `my_service`/`my_instance_id` instead, so the two paths never converge on one identity — where under Option C they do (R6).  
* Flattened exposition, default derivation: The producer declares no entities, as in R6's third scenario, so legacy label derivation applies and V1's output labels are unchanged — the entity era costs this class nothing until never-derive is opted in.

### C.1 Pros and Cons

The lists below are the delta against Option C. They override its two identity pros — declared identity always respected, and the stated pains solved — which under C.1 hold for dotted exposition only. The rest carries over unchanged: the declared-target shift for dotted exposition, merging of colliding declarations, the standardization prerequisites, the namespaced prefix, and the producer-side machinery minus covered-name recovery.

&nbsp;

Where C.1 beats C:

&nbsp;

* **No covered name is inferred from a lossy form**: Option C's objection to inferring meaning from a bare attribute name applies to no part of C.1, and the recognition cost disappears — an attribute named \`service\_name\` is neither consumed nor reinterpreted.&nbsp;&nbsp;  
* **Compatibility for the flattened class**: The declared-target shift narrows to dotted exposition, so targets behind flattening exporters keep their scrape labels end to end and the objection that Option C breaks today's default behavior stops applying to them.&nbsp;&nbsp;  
* **Smaller surface**: No underscore recovery and a narrower specification ask — permission to reverse recoverable encodings rather than to reinterpret a lossy one. Collapse and conflict handling still applies, since two wire labels can decode to one name.&nbsp;&nbsp;  
* **The guess stays available**: An operator who wants it renames `service_name` to `service.name` in the pipeline, making it an explicit per-pipeline choice. The mitigation sits downstream of the producer, since identity assignment happens inside the receiver and nothing can precede it. It must overwrite V1's already-derived value, and it cannot restore convergence in the entity era, where the scrape-target entity is already the identifying one. Like the derivation OTTL above, it is semantics-changing and intentionally outside the contract.

&nbsp;

Where C beats C.1:

* **A visible declaration is deliberately unread**: The application's identity is present in the payload and refused, so Practical Issues 1 and 3 persist for flattened exposition — under default derivation `service.name` carries the scrape-config string while the real name sits under a key no OTel consumer interprets.&nbsp;&nbsp;  
* **The declared values can be destroyed on output**: In V1 with `keep_identifying_resource_attributes=true` — Section 2's planned default — the derived covered attributes claim the `service_name` output labels and the application's values are dropped with a diagnostic, where today's consumer joins them and keeps both; UTF-8-preserving output avoids the collision entirely.&nbsp;&nbsp;  
* **No path independence for the flattened class**: Such a scraped application and the same application pushing OTLP never share an identity, and in the entity era they carry different entities (V4); dotted exposition keeps the property, per V3.&nbsp;&nbsp;  
* **The outcome follows exporter naming**: Two deployments of one application land in different identity classes depending on whether its exporter flattens, which no scrape-side setting corrects; only a pipeline rewrite does, with the caveats above.

&nbsp;

Against the document's requirements, C.1 answers differently in three places. Separate Storage is met in letter, since both identifier sets sit on one Resource under distinct keys, rather than satisfied by construction: for flattened exposition the semantic slot holds the scrape config and the application's identifier sits under a name no consumer interprets. Universal Join Key is met exactly as Option C meets it. Queryable Resource Attributes is **not met** for flattened exposition: `service.name` is absent under never-derive and holds the scrape config under derivation, so the application's value is queryable only as `service_name`. Non-Breaking Server Compatibility is met in the major-version sense, but its collision caveat is wider than Option C's. The colliding Resource is what C.1's own producers emit for this class, not only what foreign payloads carry. And with emission enabled and escaped output, Section 2's planned `keep_identifying` default makes the case reachable without further consumer configuration.

&nbsp;

The choice between C and C.1 is empirical rather than architectural: it turns on how much OTel-originated `target_info` arrives flattened rather than dotted. If UTF-8 exposition is already the norm among SDK exporters, recognition buys little and C.1 is the simpler design; if flattening dominates, recognition is what carries declared identity across the scrape, and C.1 leaves the stated pains unsolved for most targets. The quantity to measure is specifically the spelling of covered names on scraped `target_info` — not the prevalence of bare `job`/`instance` attributes in OTLP traffic, and not the prevalence of attributes literally named `service_name`, which the wire cannot reveal at all.

## Rollout

Producer emission is a configuration opt-in and defaults to disabled. Declared-target output translates

without errors on every existing consumer immediately — but its series identity shifts from the original

scrape labels to declaration-derived labels, deliberately and without a knob (see Pros and Cons); today that shift already occurs for UTF-8-exposition targets, and Option C extends it deterministically to escaped

exposition. Undeclared-target output is unchanged until never-derive is opted in; once it is, consumer

fallback support must deploy first — on a consumer without it, such Resources translate jobless with

`target_info` suppressed, exactly as service-less payloads do today. The order is therefore: Deploy consumer fallback support, then enable emission and never-derive. Flipping never-derive later changes an undeclared target's entity-era identity once (from legacy-derived labels to the pair's synthesis). Transparent intermediaries need no changes when they preserve Resource attributes; processors that drop, rename, promote, or merge them semantically must be audited before rollout. Re-exposure through a pull exporter and re-scraping behave as federation does today; `honor_labels: true` on the downstream scraper preserves whatever identity labels the exporter emitted.

&nbsp;

Standardization needs: Register `prometheus.job` and `prometheus.instance` and the scrape-target entity type in the semantic-conventions registry (one registration — the registry defines the attributes' meaning and provenance), and amend the compatibility specification, which references them and defines translation behavior — including the never-derive setting with fixed semantics and a default-off start; the specification can stabilize on that basis, since later default flips are implementation compatibility policy, not spec changes — feature-gate graduation for collector producers, and Prometheus's major version for its server-side settings, alongside Section 2's `honor_labels` and `keep_identifying_resource_attributes` flips. The `keep_identifying` flip also closes the fidelity gap where a declared identity transiting Prometheus is re-scraped without its `target_info` metadata, and the MUST-fill repeal above applies once never-derive is in effect. No recognition control or wire marker is required: nothing overrides declared identity, and the namespaced names carry their own provenance.

## Implementation Notes

Anchors as of current `main` in both repos:

* Collector `prometheusreceiver`: `CreateResource` (`internal/prom_to_otlp.go`) stores the reserved pair and stops synthesizing covered attributes from `job`/`instance`; `AddTargetInfo` (`internal/transaction.go`) consumes agreeing target metadata under the negotiated mapping profile and already skips `job`/`instance` labels. Identity completion already falls back to scrape-target context (`getJobAndInstance` in `internal/transaction.go`).  
* Collector `prometheusremotewritereceiver`: adapt its existing pair-keyed cache (`receiver.go`) to exact pair keying and stale-marker retirement per the state rules above.  
* Collector `pkg/translator/prometheusremotewrite` (`createAttributes` in `helper.go`, v1 and v2 paths) and `prometheusexporter` (`extractJob`/`extractInstance` in `utils.go`): the existing service.\*-first derivation is retained unchanged; add the pair fallback when the declared subset is absent. The pull exporter already stamps derived `job`/`instance` on all exposed series (`getMetricMetadata` in `collector.go`). Contrib currently lacks Prometheus's `keep_identifying_resource_attributes`/`promote_resource_attributes` knobs.  
* Prometheus OTLP ingestion: the existing derivation in `setResourceContext` (`metrics_to_prw.go`) is retained unchanged; add the pair fallback when `service.name` is absent. The translator's open question — `helper.go`: "XXX: Should we always drop service namespace/service name/service instance ID from the labels" — is answered by keeping the declared subset authoritative.

&nbsp;

Configuration field names are implementation-specific. Producers expose the default-disabled emission control; Remote Write receivers additionally expose a mapping profile defaulting to `exact`.

## Open Questions

* Process and timing for the semantic-conventions registration of the reserved names and the scrape-target entity type (venue resolved: the registry defines the attributes, the compatibility specification defines translation behavior).  
* Whether consumers gate the fallback, and whether any such gate ever needs a default flip given that the fallback cannot override a declaration.  
* A mechanism for relaying entity structure through Prometheus exposition (related or referenced entities), so a declared target's entity declarations survive the scrape boundary as structure rather than only as values.  
* Whether the contrib Remote Write translator should adopt upstream Prometheus's `keep_identifying_resource_attributes` and `promote_resource_attributes` for parity.  
* Whether renamed target metadata becomes a standardized, recognizable output.  
* Standardized retention and eviction behavior for push-producer cross-request association state.  
* Spec PR 4956 (bare `job`/`instance` Resource attributes) is not accepted by Prometheus maintainers, over the assumption that bare names carry Prometheus provenance — the objection Option C's namespacing answers. Should a bare-name mapping be revived, the namespaced pair remains descriptive and identity sources are never mixed.

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

Preferred: Option C — declared-first precedence, with `prometheus.job` and `prometheus.instance` as descriptive provenance and the identity fallback for Resources that declare nothing. Where a valid EntityRef set is present, the planned entity-aware mapping synthesizes identity from its declared union and no verbatim source overrides it. Acceptable fallback: Variant C.1, if covered-name recognition on `target_info` is rejected; the precedence question matters more than the recognition question, and C.1 keeps declared-first while conceding recognition.

&nbsp;

Reasoning:

* Scrape target **`job` and `instance` coordinates don’t identify the Resource.** Making the scrape coordinates the Resource's identifying attributes asserts that the Resource *is* the target, which holds only where nothing else has been asserted about it. Option C's fallback captures exactly that condition; pair-first precedence asserts it universally.  
* **Metadata consumers depend on the pair representing the Resource.** `target_info` is joined on `job` and `instance` labels: PromQL's `info()` function hard-codes them, and they are the only labels a classic join can rely on unless an operator promotes attributes expressly to match on instead. So identity cannot generally be relocated into metadata — the pair is the key through which metadata is reached. The only question is whether that key derives from what the Resource declares or from where Prometheus found it, and breaking the convention that it represents the corresponding Resource makes query results unpredictable.  
* **Declared-first overwrites no identity.** Pair-first does not eliminate identity overwriting, it inverts it: Observed scrape coordinates displace an application's declaration, the mirror image of Practical Issue 1\. Declared-first is the only order under which every Resource keeps whichever identity was asserted about it, with the pair filling the gap when none was. A partial declaration is kept on the same principle: a rule that completed it from the scrape pair would be overriding the one value the Resource did declare, and that class needs no repair — today's output already carries whichever label its declaration produces, `job` for a declared `service.name` and `instance` for a declared `service.instance.id`.  
* **Identity should not be a property of monitoring configuration.** Deriving the key from the scrape configuration re-keys an application when its scrape job is renamed, and gives one application two identities when two Prometheus servers scrape it under different job names. Option C's fallback keys undeclared targets on those same coordinates, which is the best available where nothing was declared rather than a preference: The objection is to deriving identity from the scrape configuration where a declaration exists.  
* **Retention is a separate axis from precedence.** Whether the covered attributes survive the hop as `target_info` labels is what `keep_identifying_resource_attributes` governs, and Section 2 already plans its default flip; B, C, and C.1 can each retain or drop them. A complaint that `service.*` gets stripped is an argument for that flip and for contrib parity — contrib's Remote Write translator removes all three unconditionally today — not for any precedence order here.  
* **It composes with the entity model without carve-outs.** Under the recommended policy, declared targets carry an application entity where their identity is complete and reliable, and undeclared ones the scrape-target entity, so identity synthesis applies unchanged; a partial declaration supports neither and stays entity-less; under default derivation no entities are declared and today's translation stands. Registering the pair as identifying instead is an override in effect — the producer, not the application, deciding that the declared attributes no longer identify the Resource — and declaring both makes the identifying set their union, so identity depends on the scrape configuration either way.  
* **Forward compatibility is a direction argument, not a present benefit.** The entity model formalizes the identity question rather than dissolving it — whichever design lands, something must say what identifies a scraped Resource — so a design that needs a synthesis carve-out to keep its defining property needs one permanently, where declared-first needs none. It also stands to gain: A declared target's identity synthesizes from what the application declared, so the same application pushing OTLP declares the same entity and both paths land on one identity. The terms are worth stating plainly: The entity documents and the protocol's `Resource.entity_refs` field are at Development maturity, the concrete Prometheus mapping is planned rather than published, identity scope is still open, that convergence awaits a mechanism for relaying entity structure through exposition, and byte-exact `job`/`instance` output is lost for every class once entities are declared — Option C's undeclared targets included. The synthesis assumed here is also narrow: the UUIDv5 `instance` derives from the exact referenced key/value union, with entity type, `schema_url`, key roles, and EntityRef decomposition outside that input, and equal synthesis input does not make differently typed, versioned, or decomposed declarations merge- or relay-equivalent. The claim is that Option C is the option the future need not be rewritten to accommodate, not that it pays off today.

&nbsp;

Conceded: The declared-target shift changes the output `job` for targets whose exposition flattens the covered names, which is the populous path — underscore escaping is the Go SDK exporter's unconditional default. That cost is owned in Pros and Cons, and Variant C.1 exists to avoid it if the group judges it decisive. The change cuts both ways on that path: it breaks dashboards and rules keyed on the scrape string, and it repairs consumers that read service name and namespace out of `job`, a reading that holds today only where a scrape job happens to be named for its service. Under never-derive a declaration partial in `service.instance.id` also resolves to `job` alone, so replicas of such a service converge until its exporter sets that attribute.

&nbsp;

Asking the group to settle, in order:

&nbsp;

1. **Covered-name recognition** — whether a producer may read the three underscore forms on `target_info`. This separates C from C.1 and decides the populous flattened path.  
2. **Scope** — how much of the association and proof machinery has to land before anything ships, now that it sits in the appendix rather than in the contract.

&nbsp;

Regarding identity precedence (declared identifying resource attributes or scrape target identity), Krajo's framing is the crux and I would like for him to say where it lands. If Prometheus is a resource detector making *its own* entity, then the application's entity is a different one and its identity is not replaced by the detector's — that is Option C. If instead the detected entity is the Resource's identity, that is Option B. Naming is closer to agreed: he dismisses Option A on the same provenance grounds and asks for `prometheus` semantic conventions defining `job` and `instance`, which is the reserved pair under another name.

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