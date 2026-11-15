# Smart Campaign Composer — Plan

## Context
Insider One AI Developer case'i. Pazarlamacının doğal dille yazdığı brief, Claude ile segment kuralına ve SMS/email içeriğine dönüşecek, insan onayından sonra kuyruk üzerinden mock provider'a gönderilecek ve canlı izlenecek. Stack: Vue 3 + Go + PostgreSQL, `docker compose up` + `make test`. Teslim 15 Kasım Pazar; Pazar planda yok, acil durum yedeği. En riskli ve en ağır puanlı kısım LLM çıktısının güvenilirliği (%25), bu yüzden ilk o yapılacak.

---

## 1. Gereksinim kontrol listesi (değerlendirme ağırlığıyla)

### LLM entegrasyonu — %25
- [ ] Claude çıktısı yapılandırılmış JSON (strict tool / structured output), backend'de şema + iş kuralıyla doğrulanıyor
- [ ] Segment kuralı iki seviyeli JSON DSL (bkz. §4.1). Alan/operatör whitelist'i var, backend parametreli SQL'e derliyor. LLM çıktısı asla SQL olarak çalışmıyor
- [ ] Şemada ifade edilemeyen kısıtlar (uzunluk, sayı aralığı, alan–operatör–değer uyumu) Go validator'ında kontrol ediliyor
- [ ] Brief'te karşılanamayan kriter `unsupported_criteria` olarak dönüyor, UI uyarıyor; sessizce yok sayılmıyor
- [ ] İçerik doğrulama: SMS ≤160, email konu ≤80 (şablon metin üzerinden), yalnızca whitelist'teki `{{değişken}}`'ler
- [ ] Doğrulama hatası olursa hata mesajı Claude'a geri verilip onarım deneniyor (en fazla 2 kez); yine olmazsa kontrollü hata
- [ ] SMS/email içeriği SSE ile ekrana stream ediliyor
- [ ] `LLMClient` interface'i var: gerçek Anthropic ve mock implementasyonu. Testler key olmadan çalışıyor
- [ ] Refusal / `max_tokens` / timeout / 429 durumları ele alınıyor. Model `ANTHROPIC_MODEL` env'den geliyor (varsayılan `claude-sonnet-5-5`; `claude-opus-5-5` seçilebilir)
- [ ] Key yoksa uygulama mock LLM modunda açılıyor ve UI'da "Mock LLM" rozeti görünüyor

### Mimari ve kararlar — %20
- [ ] Net bileşen sınırları: api, worker (dispatcher), mock-provider, db, web
- [ ] Kampanya durum makinesi: `draft → audience_ready → content_ready → approved → sending → completed | completed_with_failures`
- [ ] Onay anında segment, içerik ve alıcı listesi donduruluyor (snapshot)
- [ ] DECISIONS.md: her kararın nedeni ve alternatifi

### Backend güvenilirliği — %20
- [ ] Asenkron kuyruk: Postgres `deliveries` tablosu + `FOR UPDATE SKIP LOCKED`
- [ ] Kanal başına en fazla 100 msg/s (token bucket, kanal başına bir limiter)
- [ ] Idempotency: `UNIQUE(campaign_id, user_id, channel)` ve provider'a delivery id ile idempotency key. Timeout olup aslında iletilen mesaj ikinci kez gitmiyor
- [ ] Retry: üstel backoff + jitter, en fazla 5 deneme. 429'da `Retry-After`'a uyuluyor, 5xx/timeout tekrar deneniyor, diğer 4xx kalıcı hata sayılıyor
- [ ] Lease: `sending`'de takılı kalan kayıtlar lease süresi dolunca geri alınıyor (crash recovery)
- [ ] Kanal izni hem kitle çıkarılırken hem gönderim anında kontrol ediliyor
- [ ] `/healthz` (liveness), `/readyz` (DB ping), `/metrics` (Prometheus), graceful shutdown

### Frontend — %20
- [ ] Brief → segment kartı (kural açıklaması + toplam / SMS izinli / email izinli sayılar)
- [ ] Streaming içerik görünümü, düzenleme, canlı karakter sayacı + SMS segment bilgisi + değişken doğrulama
- [ ] Onay ekranı (kişiselleştirilmiş örnek önizleme), gönderim dashboard'u (kanal başına queued/sent/retrying/failed)
- [ ] Loading, hata ve boş durumları

### Test ve kod kalitesi — %15
- [ ] `make test`: Go unit + integration (Postgres) + frontend unit testleri, Docker içinde, API key gerektirmiyor
- [ ] Mock LLM senaryoları: geçerli çıktı, bozuk JSON, limit aşımı, bilinmeyen alan/değişken, refusal
- [ ] Dispatcher testleri: rate limit, retry, 429, idempotency, lease recovery
- [ ] `go vet`, `vue-tsc` temiz

### Teslim
- [ ] Repo (public), `docker compose up` (seed ve mock provider dahil), `make test`, README.md, DECISIONS.md, temizlenmiş `ai-sessions/`

---

## 2. Belirsizlikler ve varsayımlar (DECISIONS.md'ye girecek)

| # | Belirsizlik | Varsayım |
|---|---|---|
| 1 | Segment kuralı neleri ifade edebilir? | İki seviyeli DSL (§4.1): üstte AND listesi, her eleman tek koşul ya da tek koşullardan oluşan bir OR grubu. Nitelik alanları: `city`, `language`, `total_orders`, `total_spent`. Olay koşulları: `cart_add` / `purchase` için `performed` / `not_performed`, son N gün (1–365). Kanal izni segmentin parçası değil, sistem her zaman uygular; LLM izni aşan bir kural üretemez |
| 1b | Olaylar arasında sıra ("sepete ekledikten **sonra** almadı") | Desteklenmiyor. "Son 30 günde sepete ekledi" + "son 30 günde alışveriş yapmadı" olarak ifade edilir. DECISIONS'ta bilinen kısıt olarak yazılır |
| 2 | "Kaç kişiye gidecek" nasıl sayılır? | Üç sayı gösterilir: eşleşen toplam, SMS izinli, email izinli. Gönderim yalnızca izinli kanala yapılır |
| 3 | Brief DSL'in ifade edemediği bir şey isterse? | Claude `unsupported_criteria` döner, UI uyarı gösterir. Kural yine üretilir ama pazarlamacı bilerek onaylar |
| 4 | Kanal seçimi | Brief kanal belirtmiyorsa ikisi de. Pazarlamacı kanalı UI'dan kapatabilir |
| 5 | SMS limiti | İşe alım ekibiyle netleşti: şablon metin üzerinden 160 düz karakter. Segment sayısı yalnızca bilgi olarak gösterilir |
| 6 | Email gövdesi | Düz metin, en fazla 2.000 karakter (kendi limitimiz). HTML editör yok |
| 7 | Kişiselleştirme değişkenleri | Whitelist: `{{first_name}}`, `{{city}}`. Bilinmeyen değişken doğrulama hatası. Değer boşsa fallback kullanılır (`first_name` → "Değerli müşterimiz") |
| 8 | "Aynı mesaj iki kez gitmez" kapsamı | Kampanya × kullanıcı × kanal başına tek teslimat (DB unique) + provider tarafında idempotency key. Farklı kampanyalar birbirinden bağımsız |
| 9 | 100 msg/s kimde uygulanır? | Bizim dispatcher'da. Mock provider da kendi limitini aşınca 429 döner. Tek worker instance varsayılıyor; dağıtık limiter kapsam dışı |
| 10 | "Gönderildi" ne demek? | Provider 2xx (accepted) döndü demek. Delivery receipt / webhook kapsam dışı |
| 11 | Onaydan sonra düzenleme | Yapılamaz, onay snapshot alır. Onaydan önce yeniden üretme ve düzenleme serbest; düzenlenen içerik backend'de aynı validator'dan geçer |
| 12 | Zaman referansı | "Son 30 gün" = şu andan geriye, Europe/Istanbul. Seed olayları son 90 güne yayılır |
| 13 | Kimlik doğrulama | Yok, tek pazarlamacı. README'de belirtilir |
| 14 | Prompt injection | Brief güvenilmeyen girdi sayılır. Zararsızlığı prompt değil şema + whitelist + parametreli SQL garanti eder |
| 15 | `make test` ortamı | Değerlendiricide yalnızca Docker + make olduğu varsayılır; testler container'da koşar |
| 16 | İndirim / kupon | "%30" yalnızca metin içinde geçer, kupon sistemi yok |
| 17 | Kitle büyüklüğü | Seed ~5.000 kullanıcı, deterministik (sabit random seed). Telefon ve email benzersiz |

---

## 3. Kapsam

**Mutlaka (çekirdek akış)**
- Segment DSL + validator + SQL derleyici + kanal bazlı sayım
- İçerik üretimi (SSE stream) + validator + onarım döngüsü
- Gerçek Claude client + mock LLM, refusal/hata yönetimi
- Onay snapshot'ı, Postgres kuyruğu, rate limit, retry, idempotency, lease recovery
- Mock provider (ayarlanabilir hata oranı, gecikme, 429 + Retry-After, idempotency dedup, `/stats`)
- Dashboard (1 sn polling), `/healthz`, `/readyz`, `/metrics`
- Seed, docker compose, `make test`, README, DECISIONS, ai-sessions

**Vakit kalırsa**
- Bonus: CI pipeline (GitHub Actions'ta `make test`). En ucuz bonus, ~30 dk. Tek bonus bu olur
- `make eval`: 10–15 brief'lik altın set, gerçek API'ye karşı (`make test`'e dahil değil, isteğe bağlı)
- Dashboard'u polling yerine SSE ile beslemek
- Tek kanalı yeniden üret butonu, segment kuralını UI'da elle düzenleme

**Yapılmayacaklar (README'de belirtilecek)**
- Auth / multi-tenant, gerçek SMS/email provider, delivery receipt
- Zamanlanmış gönderim, A/B varyantları, kullanıcı diline göre içerik
- HTML email, unsubscribe akışı, GSM-7/UCS-2'ye göre limit
- Birden fazla worker instance ve dağıtık rate limiter, k8s

---

## 4. Mimari özet

```
web (Vue 3 + Vite + TS) ──HTTP/SSE──> api (Go)
                                      ├─ llm/        LLMClient: anthropic | mock
                                      ├─ segment/    DSL tipleri, Validate(), Compile() → SQL + args
                                      ├─ content/    Validate(), Render(), SMS segment hesabı
                                      ├─ campaign/   durum makinesi, onay snapshot
                                      └─ db (pgx)    users, events, campaigns, deliveries
worker (Go, aynı binary farklı komut) ── deliveries SKIP LOCKED → rate limiter → mock-provider
mock-provider (Go) — /sms, /email: gecikme, %X 500, %Y 429, idempotency dedup, /stats
seed (Go komutu) — compose'da migration'dan sonra bir kez çalışır
```

### 4.1 Segment DSL — iki seviye, recursion yok

```json
{
  "conditions": [
    { "type": "event", "event": "cart_add", "op": "performed", "within_days": 30 },
    { "type": "event", "event": "purchase", "op": "not_performed", "within_days": 30 },
    { "type": "any_of", "conditions": [
        { "type": "attribute", "field": "city", "op": "eq", "value": "İstanbul" },
        { "type": "attribute", "field": "city", "op": "eq", "value": "Kocaeli" } ] }
  ],
  "unsupported_criteria": [],
  "explanation": "Son 30 günde sepete ekleyip alışveriş yapmayan İstanbul/Kocaeli kullanıcıları"
}
```

- `conditions` AND ile bağlanır. Her eleman `attribute`, `event` ya da `any_of` olabilir. `any_of` yalnızca `attribute`/`event` içerir, başka bir `any_of` içeremez
- Olumsuzluk ayrı bir `not` düğümüyle değil, operatörle ifade edilir: `neq`, `not_in`, `not_performed`
- Attribute operatörleri: `eq`, `neq`, `in`, `not_in`, `gt`, `gte`, `lt`, `lte`
- JSON şemada `$defs` var ama birbirine özyinelemeli referans yok. Şema `Condition` (attribute | event) ve `Group` (`any_of: [Condition]`) tanımlarından oluşur, üst liste `Condition | Group` alır
- Şemada yok, Go validator'ında var:
  - `within_days` 1–365 aralığı, liste uzunlukları (en fazla 10 koşul, grupta en fazla 10)
  - alan–operatör–değer tipi uyumu (örn. `city` için `gt` yok)
  - Not: claude-api skill'ine göre desteklenmeyen kısıtları otomatik çıkarıp istemci tarafında doğrulayan yalnızca Python ve TypeScript SDK'ları. Go için böyle bir davranış belgelenmemiş, bu yüzden bu kısıtlar şemaya hiç konmaz
- Derleyici: her koşul tek bir parametreli SQL parçasına dönüşür. Olaylar için `EXISTS` / `NOT EXISTS`, alan adları SQL'e whitelist map'ten gelir, değerler her zaman `$n` parametresi olur

**Neden (DECISIONS.md'ye girecek):** Structured outputs recursive JSON şemasını desteklemiyor (claude-api skill, `shared/tool-use-concepts.md` → JSON Schema Limitations). İç içe and/or/not ağacı şemayla garanti edilemezdi; tek seçenek serbest JSON alıp tamamen backend'de doğrulamak olurdu. İki seviye (AND of [koşul | OR grubu]) kampanya segmentlerinin pratikte büyük kısmını karşılıyor. Böylece Claude'un çıktısı API tarafında şemaya uyması garanti edilen bir yapı oluyor, validator ve derleyici de basit ve tam test edilebilir kalıyor. `not` düğümü yerine olumsuz operatörler aynı ifade gücünü daha az belirsizlikle sağlıyor (çift olumsuzluk yok). Bedeli: yapı AND içinde OR grupları, yani CNF. Olumsuzluk operatörle ifade edildiği için mantıksal ifade gücü kaybolmuyor; örneğin (A∧B)∨C ifadesi (A∨C)∧(B∨C) olarak yazılabiliyor. Asıl kısıtlar şunlar: (1) koşul sayısı sınırları (en fazla 10 koşul, grupta en fazla 10): CNF'e çevirirken koşullar çoğalır, büyük ifadeler sınırı aşabilir; (2) olayların sırası ifade edilemiyor (§2 #1b). Bu sınırlara takılan brief'te Claude `unsupported_criteria` döner.

### 4.2 Teknoloji
- Go: `chi` router, `pgx`, `golang.org/x/time/rate`, `prometheus/client_golang`, resmi `anthropic-sdk-go`
- LLM çağrıları:
  - Segment: tek istek, yapılandırılmış çıktı, düşük effort
  - İçerik: streaming. Backend kısmi JSON'dan alanları (`sms`, `email_subject`, `email_body`) ayıklayıp SSE event'i olarak iletir, akış bitince tam doğrulama yapar
  - Risk ve geri dönüş planı: kısmi parse sorun çıkarırsa ham akış gösterilir, doğrulanmış sonuç akış bitince yerine geçer
- SDK binding'leri (strict tool / structured output, refusal fallback) implementasyon sırasında resmi Go SDK'dan doğrulanacak; tahminle kod yazılmayacak

---

## 5. Takvim (~21 saat)

Kural: her oturum sonunda `/export` → `ai-sessions/`'a koy → key ve kişisel bilgi temizliği. Bu iş Cumartesi'ye bırakılmaz.

| Zaman | İş | Bitiş kriteri |
|---|---|---|
| **Sal, 30 dk** | Plan (`docs/PLAN.md`) yazılır | Plan tamam |
| **Çar, 2,5 sa** | İlk 30 dk: güvenlik temeli (Salı'dan kaydı). `git init` (repo-yerel kimlik, LF); `.gitignore` + `.env.example`; `.claude/settings.json` (`.env` okuma deny, claude.ai connector'ları kapalı); bypass modunda da çalışan PreToolUse koruma hook'u (`.env` okuma, ortam dökümü, force push, proje dışı silme); gitleaks + `.githooks/pre-commit`; `ai-sessions/` + tarama script'i; proje CLAUDE.md. Sonra 30 dk: gofmt/vet hook. Kalan 1,5 sa: Go iskeleti, `LLMClient` interface'i, segment DSL tipleri + `Validate` + `Compile` (saf fonksiyonlar) + unit testler | `.env` okunamıyor/commit'lenemiyor, sahte key commit'te yakalanıyor; segment testleri yeşil |
| **Per, 2 sa** | Anthropic client (yapılandırılmış çıktı, refusal, timeout), onarım döngüsü, mock LLM senaryoları; içerik şeması + validator (limitler, değişkenler). Gerçek API çağrısı yok: Console kredisi en sonda yüklenecek | Key olmadan LLM pipeline (mock) testleri yeşil |
| **Cum, 2 sa** | Postgres şeması + migration, seed, compose (db + api + seed); `POST /campaigns` (brief → segment + sayılar), `GET /campaigns/:id/content/stream` (SSE) | **Kilometre taşı:** LLM kısmı mock LLM ile curl üzerinden uçtan uca çalışıyor |
| **Cmt 00–03** | Onay + snapshot, deliveries kuyruğu, worker (SKIP LOCKED, limiter, retry, lease), mock provider; testler | Kaos ayarlı provider'a 5k mesaj, çift gönderim yok, ≤100/s |
| **Cmt 03–04** | Stats endpoint, `/metrics`, `/healthz`, `/readyz`, graceful shutdown | |
| **Cmt 04–09** | Frontend: composer akışı, streaming, düzenleme + sayaçlar, onay, dashboard | UI'dan tam akış çalışıyor |
| **Cmt 09–10** | Temiz klondan `docker compose up` ile uçtan uca prova, hata düzeltme | |
| **Cmt 10–11** | Integration testler, `make test`'in Docker içinde çalışması | Temiz ortamda `make test` yeşil |
| **Cmt 11–13** | README, DECISIONS.md, ai-sessions son temizlik (key/email taraması) | |
| **Cmt 13–13:30** | Console kredisi yüklenir, 5 brief ile gerçek API smoke testi (key yalnızca `.env`'de); bulgular düzeltilir | Gerçek API ile 5/5 geçerli çıktı |
| **Cmt 13:30–14** | Yalnızca her şey yeşilse CI bonusu, değilse tampon (smoke testi bulguları için de) | |
| **Cmt 14–15** | Son temiz klon doğrulaması, push, linki gönder | |
| **Pazar** | Yalnızca acil durum | |

**Kesme noktaları**
- Cuma sonunda LLM kısmı curl ile çalışmıyorsa: kısmi JSON stream'i bırakılır, ham stream + sonda doğrulama yapılır.
- Cumartesi 09'da frontend bitmediyse: dashboard basit tabloya indirilir, düzenleme yalnızca textarea + sayaç olur.

---

## Revize takvim

Yukarıdaki §5 takvimi (15 Kasım teslim) olduğu gibi duruyor; teslim **17 Kasım Salı gün sonuna** kaydı, bu yüzden aşağıdaki takvim geçerli.

| Gün | İş |
|---|---|
| **Cuma (13 Kasım)** | Go iskeleti ve segment DSL (bitti), LLM katmanı. Gece: DB, seed, mock provider, kuyruk ve worker, Docker içinde `make test` |
| **Cumartesi (7–8 sa)** | Kampanya API'si (brief → segment ve sayılar, içerik SSE, düzenleme, onay ve snapshot), `/healthz` `/readyz` `/metrics`, compose'a api; ardından frontend'e başlangıç. Gece: frontend ilk sürüm |
| **Pazar (5–6 sa)** | Frontend, temiz klondan uçtan uca prova, gerçek API smoke testi. Gece: README ve DECISIONS taslakları, CI, eksik testler |
| **Pazartesi (3 sa)** | Son kontroller, temiz klonda `docker compose up` ve `make test`, son tarama, push |
| **Salı (17 Kasım)** | Yalnızca teslim maili |

## 6. Doğrulama
- `make test` (Docker içinde, key yok): segment validator/derleyici, içerik validator, onarım döngüsü (mock LLM), retry/backoff politikası, dispatcher integration (Postgres + kaos ayarlı mock provider): çift gönderim yok, rate ≤100/s/kanal, izinsiz kullanıcıya gönderim yok
- Temiz klonda `docker compose up` → UI'da örnek brief ile akışı baştan sona çalıştır; dashboard'da retry ve 429'ların göründüğünü ve sonunda tüm teslimatların sonuçlandığını gör
- Mock provider `/stats` ile DB sayılarını karşılaştır (provider'da benzersiz idempotency key sayısı = DB'deki `sent`)
- Gerçek key ile örnek brief + 4 farklı brief (şehir yok, kanal belirtilmiş, desteklenmeyen kriter, uzun içerik isteği) manuel smoke testi
- Push öncesi: `ai-sessions/` ve tüm repoda `sk-ant-`, email ve telefon taraması
