# Vukem — revisão de arquitetura, código, segurança e documentação

Data: 2026-10-24. Base revisada: `main` em `cf7e016` (release `v0.30.0`). Acesso: código-fonte local completo e fonte do site (`site/content/docs`, 60 páginas MDX). O site publicado em vukem.dev não foi acessado pela rede; a análise usa o fonte das páginas no repositório.

Premissa fixa: a identidade do Vukem é MVC (controllers, requests, models, views, resources) sobre `net/http`. Nenhuma recomendação abaixo troca esse padrão. Nada de código ou documentação foi alterado nesta etapa.

## 1. Resumo executivo

O Vukem está em BETA avançado com uma base acima da média para a idade do projeto: 352 arquivos de teste para 318 de código, 265 funções `TestSecurity_*`, gate de cobertura (80% / 90%), `apidiff`, `govulncheck`, release assinado. Os defaults de segurança verificados (CSRF por Fetch Metadata, cookies `Secure` obrigatórios em produção, argon2id, JWT com allowlist, `os.Root` em storage, SSRF bloqueado em `httpclient`, segredos redigidos) estão corretos no código.

Riscos principais, em ordem: (1) `z.FromTags`/`api.Register` não aceitam `map[string]V` tipado, o que molda APIs inteiras nos projetos consumidores; (2) `cache.Cache.Store()` devolve o backend sem prefixo e sem aviso, abrindo colisão de namespace entre features; (3) deriva entre documentação e código em `docs/http.md` e `docs/validation.md`; (4) superfície pública ampla (cerca de 50 pacotes no módulo raiz) a congelar antes do compromisso Go 1 de `v1`.

Dos 11 feedbacks: 5 confirmados (FB-1, FB-2, FB-3, FB-4, FB-10), 3 parciais (FB-6, FB-7, FB-9), 3 refutados por já estarem implementados ou documentados (FB-5, FB-8, FB-11). Prioridade de execução: Fase 0 (docs, 1 dia) e Fase 1 (`z`, mapas e normalização) primeiro; depois cache, auth e preparação para `v1`.

## 2. Mapa de achados

Severidade: crítica / alta / média / baixa. Esforço: P (≤ 1 dia), M (1–3 dias), G (> 3 dias). Status: confirmado, parcial, refutado, não verificado.

| ID | Área | Severidade | Esforço | Status | Referência |
| --- | --- | --- | --- | --- | --- |
| FUN-01 (FB-1) | Validação | alta | M | confirmado | `z/tags.go:398-399`, `z/tags.go:24-32`, `api/typed.go:135` |
| FUN-02 (FB-2) | Cache | média | P | confirmado | `cache/cache.go:33`, `cache/cache.go:59` |
| FUN-03 (FB-3) | Validação | baixa | P | confirmado | `z/schema.go:203-216`, `z/composite.go:506` |
| FUN-04 (FB-4) | Validação / senha | baixa | P | confirmado | `z/string.go:250`, `z/string.go:318` |
| FUN-05 (FB-5) | Factory | baixa | P | refutado (parcial: `Pick` ponderado) | `factory/locale.go:17-20`, `factory/faker.go:86`, commit `9c5bf18` |
| FUN-06 (FB-6) | Seeders | baixa | P | parcial | `database.go:239-274`, `docs/database.md:387` |
| FUN-07 (FB-7) | web.Ctx | baixa | — | parcial (manter) | `web/response.go:176-180`, `docs/api.md:104` |
| DOC-07 (FB-8) | Docs validação | baixa | — | refutado | `z/schema.go:47-59`, `docs/validation.md:177`, `site/.../validation.mdx:446,483` |
| SEG-04 (FB-9) | Logging / panic | baixa | P | parcial | `middleware/middleware.go:57`, `logging/telegram/telegram.go:43,55` |
| FUN-08 (FB-10) | contrib/imaging | baixa | G | confirmado | `contrib/imaging/imaging.go:29,234-235` |
| FUN-09 (FB-11) | vukemtest | baixa | — | refutado | `vukemtest/client.go:233`, `vukemtest/client.go:523`, `docs/testing.md:68` |
| ARQ-01 | Raiz `vukem` como composition root | média | M | confirmado | `app.go` (1.722 linhas), 43 `With*` na raiz |
| ARQ-02 | `web.Services` no contexto (service locator) | baixa | P | confirmado | `web/ctx.go:47` (15 campos), `middleware/middleware.go:52` |
| ARQ-03 | Módulos aninhados presos a core antigo | baixa | P | confirmado | `contrib/stripe/go.mod`, `drivers/s3/go.mod`, `drivers/websocket/go.mod` (`v0.19.0`) |
| ARQ-04 | Superfície pública antes de `v1` | média | G | confirmado | `ls` da raiz (50 diretórios de pacote), `docs/stability.md:3` |
| ARQ-05 | SQLite no `go.mod` do core | baixa | — | confirmado (decisão) | `go.mod:8` |
| COD-01 | Erros descartados com `_ =` | baixa | P | confirmado | `auth/middleware.go:73,234,272` |
| COD-02 | Helpers duplicados | baixa | P | confirmado | `i18n/middleware.go:156`, `middleware/secure.go:459`, `admin/values.go:85` |
| COD-03 | Arquivos grandes sem linter de complexidade | baixa | P | confirmado | `.golangci.yml` (sem `gocognit`/`funlen`), `z/tags.go` (721 linhas) |
| COD-04 | Snippets do site não compilam em CI | média | M | confirmado | `Taskfile.yml:191` (`site:build` só), `internal/tools/` |
| COD-05 | Convenção funcional implícita | baixa | P | confirmado (positivo) | `z/string.go:123-336`, `orm/query.go:154`, `router/*.go` |
| SEG-01 | `Store()` sem prefixo permite colisão | média | P | confirmado | `cache/cache.go:59-61`, `site/.../cache.mdx:196` |
| SEG-02 | Throttle de login só nos scaffolds | média | M | confirmado | `auth/auth.go:164`, `internal/cli/scaffolds/auth-web/.../account.go.tmpl:29` |
| SEG-03 | `ratelimit.ByIP` atrás de proxy | baixa | P | confirmado (docs) | `web/ctx.go:265-267`, `middleware/middleware.go:108` |
| SEG-05 | Página de debug e corpos de formulário | baixa | P | não verificado | `web/debug.go:19-24` |
| DOC-01 | `docs/http.md` lista métodos inexistentes em `Ctx` | média | P | confirmado | `docs/http.md:83-88` vs `web/*.go` |
| DOC-02 | Assinatura de `z.Map` errada na doc | baixa | P | confirmado | `docs/validation.md:78` vs `z/composite.go:180` |
| DOC-03 | Tabela de tags omite `any` e `map[string]any` | baixa | P | confirmado | `z/tags.go:24-32`, `docs/validation.md` (sem menção), commit `999d0e8` |
| DOC-04 | `make:seeder` não gera `AppSeeder` | baixa | P | confirmado | `internal/cli/*.go` (sem `AppSeeder`) |
| DOC-05 | `PLAN.md` com status desatualizado | baixa | P | confirmado | `docs/PLAN.md` (Status: `v0.20.1`, 2026-10-17) vs tag `v0.30.0` |
| DOC-06 | Exemplos presos a core `v0.20.0` | baixa | P | não verificado (impacto nos tutoriais) | `examples/api/go.mod`, `examples/saas/go.mod` |

## 3. Achados por área

### 3.1 Arquitetura e acoplamento

**ARQ-01 — Raiz como composition root grande.** O pacote `vukem` soma 6.277 linhas em 8 arquivos (`app.go` 1.722, `http.go` 1.125, `background.go` 895) e expõe 43 opções `With*` (22 em `app.go`, 9 em `services.go`, 5 em `database.go`, 5 em `background.go`, 2 em `http.go`), importando 25 subpacotes. Isso é esperado num framework MVC (é o `bootstrap/app.php` do Laravel), e a regra "core nunca importa `drivers/*` ou `contrib/*`" está cumprida (o único hit de `grep` é texto de ajuda em `internal/cli/rpc.go:64`, não import). O custo é de descoberta: quem lê `app.go` não encontra as opções agrupadas por subsistema. Recomendação: sem mudar API, reorganizar os arquivos da raiz por ciclo de vida (`options_*.go` por subsistema, `boot.go`, `serve.go`, `validate_config.go`) e publicar uma tabela única "opção → subsistema → doc" em `docs/configuration.md`. Trade-off: só move código; `apidiff` garante zero quebra. Teste: `task api:check` sem diferença.

**ARQ-02 — `web.Services` como service locator por contexto.** `web.Services` (`web/ctx.go:47`, 15 campos) viaja no `context` e é lido por middlewares (`middleware/middleware.go:52`, `web.ServicesFrom(r.Context()).Log`). É a escolha que evita globais e `init()` (objetivo declarado em `docs/architecture.md`) e é coerente com Go idiomático. O risco é o crescimento silencioso do bag. Recomendação: fixar em `docs/architecture.md` a regra "um campo entra em `Services` só se for lido por mais de um pacote; demais dependências entram por construtor". Baixo esforço, evita que vire um `app()` global do Laravel.

**ARQ-03 — Módulos aninhados exigem core antigo.** `contrib/stripe`, `drivers/s3` e `drivers/websocket` exigem `vukem v0.19.0`; `contrib/imaging` `v0.22.0`; `contrib/graphql`, `contrib/grpc`, `drivers/otel`, `drivers/redis` `v0.25.0`; o core está em `v0.30.0`. `task mod:check` prova que compilam com `GOWORK=off`, mas significa que o driver é testado contra uma API de 11 releases atrás fora do workspace. Na prática o app resolve o core mais novo por MVS, e `apidiff` protege compatibilidade, então o impacto hoje é baixo. Recomendação: no `task release`, elevar o `require` dos módulos aninhados para a versão liberada quando o core mudou algo que eles usam (ou sempre, a cada minor). Critério: `grep "vukem v" */*/go.mod` mostra uma única versão após a release.

**ARQ-04 — Superfície pública antes de `v1`.** O módulo raiz publica cerca de 50 pacotes, incluindo utilitários (`ulid`, `uuid`, `cpu`, `archive`, `export`, `mediatype`, `signed`, `social`, `htmx`). `docs/stability.md:3` promete compatibilidade Go 1 em `v1.x`. Cada pacote público vira contrato permanente. Isto é a tarefa `B.5.1` já prevista. Recomendação: para cada pacote pequeno decidir "público, `internal/`, ou `contrib/`", registrando em `docs/api/review.md`; critério: nenhum pacote público sem página própria no site ou seção em `docs/`. Nenhum impacto na estrutura MVC.

**ARQ-05 — SQLite no core.** `go.mod:8` traz `modernc.org/sqlite` (e `libc`, `memory`) para todo app, mesmo os que usam Postgres. É uma decisão consciente (zero-config em desenvolvimento). Trade-off: binário e grafo de dependências maiores para todos; benefício: `vukem new` funciona sem Docker. Não recomendo mudar agora; registrar em `docs/brainstorming.md` se ainda não estiver explícito (pendência humana).

### 3.2 Qualidade de código

**COD-01 — Erros descartados.** 112 ocorrências de `_ = f(...)` e 110 `//nolint` no código não-teste do core. A maioria é legítima (`Flash` best-effort em `admin/handlers.go:670,728,789`, `Close` em defer). Exceções a revisar: `_ = c.Redirect(target)` em `auth/middleware.go:73,234,272` descarta erro de escrita sem log. Recomendação: logar em `Debug` quando `Redirect` falhar, ou documentar no comentário por que ignorar é seguro. Critério: `grep -c "_ = c.Redirect" auth/middleware.go` igual a 0 ou cada ocorrência com comentário de uma linha.

**COD-02 — Helpers duplicados.** `orDefault` existe em `i18n/middleware.go:156` e `middleware/secure.go:459` (ambos substituíveis por `cmp.Or`, já usado em `middleware/secure.go:136`); `truncate` em `admin/values.go:85`, `devtools/devtools.go:437`, `logging/telegram/telegram.go:658`; `loopbackHost`/`isLoopback` em `devtools/access.go:45` e `drivers/mysql/mysql.go:114`. A duplicação entre módulos diferentes (`drivers/mysql`) é aceitável: criar um pacote `internal/strutil` compartilhado não alcança módulos separados. Recomendação: trocar `orDefault` por `cmp.Or`; unificar os três `truncate` só se tiverem a mesma semântica (os de `devtools` e `telegram` recebem um argumento; o de `admin` dois). DRY aqui é oportunidade, não obrigação.

**COD-03 — Arquivos grandes e sem linter de complexidade.** Maiores arquivos não-teste: `app.go` 1.722, `http.go` 1.125, `contrib/graphql/parser.go` 1.022, `orm/query.go` 909, `background.go` 895, `admin/handlers.go` 795, `z/tags.go` 721. `.golangci.yml` habilita `gocritic`, `revive`, `gosec` e outros, mas nenhum limite de complexidade (`gocognit`, `cyclop`, `funlen`). Recomendação: habilitar `gocognit` com limite generoso (30) para medir a tendência, e dividir `z/tags.go` por kind (`tags_string.go`, `tags_number.go`, `tags_list.go`, `tags_map.go`), o que também é o caminho natural para FUN-01. Critério: `golangci-lint run` limpo com o novo linter; nenhum arquivo de `z/` acima de 500 linhas.

**COD-04 — Snippets do site não são compilados.** Um script desta revisão extraiu todos os blocos ```go das 60 páginas MDX e cruzou identificadores `pkg.Nome` e `c.Metodo()` com a API exportada; não houve divergência real (os 30 "desconhecidos" são variáveis locais chamadas `c`, `api`, `mail`). Isso é bom, mas é manual. `Taskfile.yml:191` só roda `site:build`; nada compila os exemplos. Recomendação: um teste em `internal/tools/` (ou `site/scripts`) que extrai blocos com `title="...go"` e roda `go vet` num módulo temporário que aponta para o core via `replace`. Trade-off: alguns blocos são fragmentos; marcar com um atributo (`nocompile`) para excluir. Critério: `task ci` falha quando um snippet referencia identificador inexistente.

**COD-05 — Convenção funcional já existe, falta nomeá-la.** Schemas `z` (`z/string.go:123-336`), `orm.Query[T]` (`orm/query.go:154` e seguintes) e `factory.Factory[T]` são builders imutáveis por valor: cada método devolve uma cópia, o que a página do site já explica para factories ("Factories are values", `factories-seeders.mdx:136`). `router.Route`/`Group` usam ponteiro (14 métodos), coerente com registro mutável. `errs` tem tipos explícitos (`HTTPError`, `ValidationError`, `Status`, `StatusCoder`). Recomendação (programação funcional sem forçar estilo): escrever em `docs/architecture.md` a regra "descrições são valores imutáveis (schemas, queries, factories, configs); registradores e recursos são ponteiros (router, app, stores)". Opcional: `Transform`/`Refine`/`Preprocess` já são funções puras; documentar que não devem tocar I/O (a doc do site já aponta `Refine` com banco como exceção explícita em `validation.mdx:396`).

### 3.3 Segurança (resumo; detalhes na seção 6)

Baseline verificado e correto: CSRF por `http.CrossOriginProtection` com fallback de token em tempo constante (`csrf/csrf.go:54,124`); sessão `Secure` obrigatória em produção, `SameSite=Lax`, id via `rand.Text`, idle 2h e vida 24h, regeneração no login (`session/manager.go:80,103,151,229`, `auth/session.go:141`); argon2id padrão (`auth/hash/hash.go:1`); JWT com allowlist por chave e sem `none` (`auth/jwt/jwt.go:2-3`); tokens e códigos de recuperação comparados em tempo constante (`auth/token.go:245`, `auth/totp/totp.go:214`); upload com sniffing, allowlist de extensão e guarda de bomba de descompressão (`upload/upload.go:277`, `upload/image.go:17-19`); storage local em `os.Root` (`storage/local.go:18`); `nosniff` ao servir (`storage/serve.go:57`); SSRF bloqueado (`httpclient/httpclient.go:91`); segredos redigidos em `fmt`, `slog` e JSON (`config/secret.go`); produção recusa `APP_DEBUG` e devtools (`app.go:951-956`); `TrustForwardedProto` exige `TrustedProxies` (`middleware/secure.go:79-80`); CORS recusa `*` com credenciais (`middleware/cors.go:35-36`); CRLF rejeitado em cabeçalhos de e-mail (`mail/mail.go:194,258`); ORM valida colunas em `Update` e placeholders em `WhereRaw` (`orm/write.go:300-303`, `orm/query.go:230-251`); admin ordena só por colunas allowlistadas e nega tudo sem `Authorizer` (`admin/handlers.go:182`, `admin/admin.go:85-86`).

Lacunas encontradas: SEG-01 (namespace do cache), SEG-02 (throttle de login não é do framework), SEG-03 (documentação de `ByIP` atrás de proxy), SEG-04 (roteamento de panics), SEG-05 (não verificado).

### 3.4 Funcionalidades ausentes

Detalhadas na seção 4 (FB-1, FB-2, FB-3, FB-4, FB-10) e no plano. Acrescento duas que os feedbacks não cobrem:

- **FUN-10 — `make:seeder --app`.** `grep AppSeeder internal/cli/*.go` não retorna nada: o scaffold só gera `Seeder`. Gerar `AppSeeder` por flag resolve a confusão relatada em FB-6 na origem.
- **FUN-11 — `factory.Pick` ponderado.** `factory.Pick` (`faker.go:86`) é uniforme. Um `PickWeighted(f, []Weighted[T])` de 10 linhas cobre o pedido de FB-5 que ainda falta.

### 3.5 Documentação

- **DOC-01** `docs/http.md:83-88` lista em `Ctx`: `Accepts(...)`, `BindForm`, `User()`, `Auth()`. Nenhum existe como método de `*web.Ctx` (lista real extraída de `web/*.go`: `BindJSON`, `Input`, `QueryInput`, `Authorize`…). O equivalente real é `web.Bind[T](c)` (`web/parse.go:33`) e `auth.User[U](c, guard)` (`auth/auth.go:193`). A tabela também omite `Created`, `Accepted`, `Problem`, `Blob`, `Status`, `NotModified`, `CheckIfMatch`. A página do site `basics/responses.mdx` está correta (`:113`, `:414`); o desvio está só em `docs/`.
- **DOC-02** `docs/validation.md:78` descreve `z.Map(key, val)` produzindo `map[K]V`; o código é `z.Map(value AnySchema)` com chaves string (`z/composite.go:178-180`). O site está certo (`validation.mdx:189`).
- **DOC-03** A tabela de kinds em `z/tags.go:24-32` e `docs/validation.md` não mencionam que `any` e `map[string]any` são aceitos desde `999d0e8` (2026-10-18; código em `z/tags.go:277-300`). O site documenta (`validation.mdx:193-206`). Como `TestTagRules_*` compara a tabela com a implementação, vale incluir os kinds no mesmo teste.
- **DOC-04** Doc de seeders está completa (`docs/database.md:387`, `factories-seeders.mdx:28`); falta só o scaffold (FUN-10).
- **DOC-05** `docs/PLAN.md` Status: "Last release `v0.20.1`", "Updated 2026-10-17"; a tag mais recente é `v0.30.0` (2026-10-19). O `release` tool deveria atualizar essa linha.
- **DOC-06** `examples/api` e `examples/saas` exigem `vukem v0.20.0`; `examples/blog` `v0.29.0`. Os tutoriais em `docs/tutorials/` apontam para esses exemplos. Não verifiquei trecho a trecho se os tutoriais citam APIs posteriores a `v0.20.0`; marcado como pendência.
- **DOC-07** (FB-8) já coberto: ver seção 4.

## 4. Feedbacks recebidos

Legenda de ação: **framework** (mudança no Vukem), **documentação**, **projeto local** (o consumidor resolve sozinho).

### FB-1 — `z.FromTags` e `api.Register` não aceitam campo map

- **Veredito: confirmado** (alta).
- **Evidência:** `z/tags.go:398-399` é o `default` do switch de kinds e devolve `unsupported type %s`; a tabela de regras em `z/tags.go:24-32` não tem linha para map; `api/typed.go:135` chama `z.For[In]()` no boot, então o panic acontece ao registrar a rota. `z.Map` existe (`z/composite.go:180`) e o OpenAPI já serializa map (`openapi/schema.go:106-107`). Desde `999d0e8` (2026-10-18), `map[string]any` e `any` são aceitos sem regras (`z/tags.go:277-300`): o caso do projeto (`map[string]string`) continua falhando. Não reproduzi o panic num teste descartável porque o `default` do switch é inequívoco.
- **Ação: framework.** Adicionar `case t.Kind() == reflect.Map && t.Key().Kind() == reflect.String` que constrói `z.Map(valueSchema)` com o valor tipado via recursão, aceitando `min`/`max` (quantidade de entradas) e `dive` (regras nos valores). Mensagem do `default` deve listar os kinds aceitos. Atualizar `z/tags.go:24-32`, `docs/validation.md` e `validation.mdx:186`. Trade-off: chaves não-string ficam fora (JSON só tem chave string). Teste: `TestTagRules_Map` com `map[string]string validate:"max=2,dive,min=1"` e caso de panic para `map[int]string`.
- **Workaround do projeto** (lista de pares) é local e pode ser revertido depois.

### FB-2 — `cache.Cache` sem add atômico tipado; `Store()` sem prefixo

- **Veredito: confirmado** (média).
- **Evidência:** `cache.Store` tem `Add` (`cache/cache.go:33`), mas `Cache` expõe `Get[T]`, `Put`, `Forget`, `Flush`, `Increment`, `Remember[T]`, `NewLock` (`cache/cache.go:64-185`); `(*Cache).Store()` (`:59`) devolve o backend cru, e `key()` (`:61`) aplica o prefixo só nos métodos de `Cache`. A doc do site descreve `c.Store()` como "The backend, for advanced checks" sem aviso (`cache.mdx:196`).
- **Ação: framework + documentação.** `Add[T any](ctx, c, key, v, ttl) (bool, error)` e `Pull[T any](ctx, c, key) (T, bool, error)` com prefixo e JSON como `Get[T]`. `Pull` não é atômico em `Store` (get + delete): documentar que é "get-and-forget", não "claim"; para claim exclusivo usar `Add`. Em `Store()`, godoc e doc do site: "sem prefixo e sem codificação; use só para checagens de backend". Teste: `TestCache_Add_OnlyFirstWins` concorrente em `cachetest.Run` (memória, DB, Redis).
- O workaround do projeto (`devicelink:` manual) é local; migra para `cache.Add` quando existir.

### FB-3 — `Check` valida mas não devolve o valor normalizado

- **Veredito: confirmado** (baixa).
- **Evidência:** `checkValue` (`z/schema.go:203-216`) chama `s.parseAny` e descarta o valor, devolvendo só `c.issues`; `StructSchema[T].Check` (`z/composite.go:506`) retorna `error`. `docs/validation.md:95` sugere `Preprocess` para "validar um valor normalizado", o que não cobre `Default`/`Transform`.
- **Ação: framework.** `Normalize(v T) (T, error)` em `StructSchema[T]` (e `Schema[T]` se não quebrar implementações externas; `apidiff` decide) reaproveitando `normalizeValue` + `parseAny` + decodificação para `T`. Documentar em `Check` que ele não transforma. Teste: `Trim().Min(2)` com `" ab "` devolve `"ab"`. Grafia: o repositório usa inglês americano (`normalizeValue`), então `Normalize`, não `Normalise`.

### FB-4 — Sem regras de classe de caractere para senha

- **Veredito: confirmado** (baixa).
- **Evidência:** métodos de `StringSchema` em `z/string.go` (`Min`, `Max`, `Len`, `Email`, `URL`, `UUID`, `ULID`, `Alpha`, `Alphanum`, `Numeric`, `Regex` `:250`, `StartsWith`, `EndsWith`, `Includes`, `OneOf`, `Refine` `:318`…); nada de classes de caractere. RE2 não tem lookahead, então uma regex não resolve.
- **Ação: framework, opt-in.** `HasLower()`, `HasUpper()`, `HasDigit()`, `HasSymbol()` emitindo código `invalid_string` com chave `string.has_lower` etc., traduzíveis como as demais. Trade-off: NIST SP 800-63B desencoraja regras de composição; o default dos scaffolds (comprimento mínimo 12) deve continuar, com as regras novas como opção para projetos com exigência de compliance. Não criar `Password(opts)`: é abstração especulativa. Teste: tabela com `"abc"`, `"Abc1!"`, unicode (`"Ábc1!"` conta como maiúscula?) e mensagens localizadas em `localize_test.go`.
- O `Refine` do projeto é local e continua válido até a release.

### FB-5 — `factory.Faker` sem dados de locale

- **Veredito: refutado** (já entregue), parcial para `Pick` ponderado.
- **Evidência:** `factory/locale.go:17,20` (`LocaleEN`, `LocalePTBR`), `faker.go:211` `Company`, `:219` `ProductTitle`, `:228` `ProductDescription`, `:235` `Category`, `:239` `StreetAddress`, `:246` `City`, `:274` `FakeCPF`; commit `9c5bf18` (2026-10-17, `feat(factory): add en and pt-BR locales with commerce and address data`), publicado em `v0.30.0`. Site documenta em `factories-seeders.mdx:63,143`. `Pick` (`faker.go:86`) é uniforme; não há `Weight` no pacote.
- **Ação: projeto local** (atualizar para `v0.30.0` e remover os arquivos de vocabulário) + **framework** pequeno: `PickWeighted`. Teste: distribuição com seed fixa.

### FB-6 — Seeder recebe só `*database.DB`

- **Veredito: parcial.**
- **Evidência:** duas interfaces em `database.go:239-260`; `NamedAppSeeder` (`:265-274`); `Name()` é interface opcional lida em `database.go:371`. A doc já apresenta `AppSeeder` ao lado de `Seeder` com o critério "precisa de config, logger ou serviços" (`docs/database.md:387`; `factories-seeders.mdx:28`). O que falta é o scaffold: `make:seeder` não gera `AppSeeder` (nenhuma ocorrência em `internal/cli/*.go`).
- **Ação: framework (CLI).** `vukem make:seeder Demo --app` gera `SeedApp(ctx, app)`. Não unificar as interfaces: `Seeder` puro é testável com um `*database.DB` sem bootar o app, e isso é um valor. Teste: `internal/cli` golden file do template com `--app`.

### FB-7 — `c.NoContent()` não recebe status

- **Veredito: parcial** (comportamento confirmado, mas já documentado; manter).
- **Evidência:** `web/response.go:176-180`, godoc "NoContent writes 204."; `docs/api.md:104` e `responses.mdx:113,414` mostram 204. `c.Status(code)` existe para o caso "status sem corpo" (`responses.mdx:158`).
- **Ação: nenhuma no framework.** Um `NoContent(status...)` variádico convidaria `NoContent(200)`, que contraria o nome. Se quiser reduzir a dúvida, a godoc pode acrescentar "for another empty status use c.Status(code)". Projeto local: usar `c.Status`.

### FB-8 — Mensagens de validação misturam texto e chaves i18n

- **Veredito: refutado** no código atual.
- **Evidência:** desde `v0.25` um erro genérico de `Refine` **não** é tratado como chave: `pctx.addErr` (`z/schema.go:47-59`) usa a mensagem fixa `invalid` e guarda o erro em `Issue.Err` ("Fail closed: the text of an arbitrary error may be internal"). Só `z.FieldErrorKey` é chave traduzível (`z/issues.go:146-151`). Está documentado em `docs/validation.md:134,151,177` e no site em "Changing a message" (`validation.mdx:446`) e "Translated messages" (`:483`).
- **Ação: projeto local.** Se o projeto depende de `errors.New(key)` ser traduzido, isso quebrou em `v0.25` por design; migrar para `z.FieldErrorKey`. Nenhuma mudança de doc necessária; opcionalmente um aviso "antes de v0.25…" já existe em `docs/validation.md:177`.

### FB-9 — Nível crítico/panic para o handler do Telegram

- **Veredito: parcial.**
- **Evidência:** `Recover` loga em `ERROR` com stack (`middleware/middleware.go:57`); `telegram.LevelFatal` existe (`telegram.go:43`) e `ChatIDs map[slog.Level]string` roteia por nível (`:55`), documentado em `docs/subsystems.md:56-69`. O que falta é um sinal distinto para panic.
- **Ação: framework (pequena).** Não logar em `LevelFatal`: Fatal implica encerramento e o processo continua. Preferir um atributo estável `slog.Bool("panic", true)` no registro de `Recover` e, na doc do Telegram, um exemplo de `Filter` que roteia `panic=true` para outro chat. Teste: `TestRecover_LogsPanicAttr`. Trade-off: zero mudança de nível, compatível com qualquer handler.

### FB-10 — Encoder WebP em `contrib/imaging`

- **Veredito: confirmado** (baixa, esforço alto).
- **Evidência:** `contrib/imaging/imaging.go:29` importa `golang.org/x/image/webp` (só decode; `:234-235`). `x/image` não tem encoder WebP.
- **Ação: framework, condicionada à política de dependências.** Opções: (a) dependência externa pure-Go (ex.: implementação via WASM do libwebp) em `contrib/imaging` — passa pela política de `docs/architecture.md` e adiciona ~MBs; (b) encoder lossless próprio (VP8L) — semanas de trabalho; (c) manter saída PNG/JPEG e documentar. Recomendo (c) agora e registrar (a) em `brainstorming.md` como decisão pendente. Teste, se feito: round-trip decode(encode(img)) com `go-cmp`.

### FB-11 — Helpers de JSON em `vukemtest`

- **Veredito: refutado** (já existe).
- **Evidência:** `Response.JSON(v any)` (`vukemtest/client.go:233`, commit `9f218a9`, 2026-10-18), `AssertJSON(want)` (`:523`), `AssertJSONPath` (`:482`), `AssertJSONCount` (`:498`), `JSONPath` (`:429`); documentados em `docs/testing.md:68` e `site/.../testing/overview.mdx`.
- **Ação: projeto local** (atualizar e usar `.JSON(&v)`). Não adicionar `JSONInto`: seria sinônimo.

### Itens "Looked at and not filed"

Mantidos fechados. Nenhuma evidência nova para `Op.Status`, `errs.NewKey`, `Response()`, `inertia.Once` ou o probe Windows (código do projeto).

## 5. Plano de execução

Cada item é executável por um agente sem contexto adicional. "Aceite" é o comando ou teste que prova a conclusão. Dependências explícitas em `needs:`.

### Fase 0 — Documentação e higiene (1 dia, sem dependências)

- [ ] **0.1** Corrigir `docs/http.md:83-88`: remover `Accepts`, `BindForm`, `User()`, `Auth()`; apontar para `web.Bind[T]` e `auth.User[U]`; acrescentar `Created`, `Accepted`, `Problem`, `Blob`, `Status`, `NotModified`. Aceite: um teste em `web` que lista métodos exportados de `*Ctx` por reflexão e falha se `docs/http.md` citar um método inexistente (mesmo padrão de `TestTagRules_*`).
- [ ] **0.2** Corrigir `docs/validation.md:78` para `z.Map(value)` → `map[string]V`. Aceite: `rg "z.Map\(key" docs` vazio.
- [ ] **0.3** Acrescentar `any` e `map[string]any` à tabela em `z/tags.go:24-32` e a `docs/validation.md`; estender `TestTagRules_*` para cobrir kinds. Aceite: `go test ./z -run TestTagRules` verde.
- [ ] **0.4** Documentar `Store()` como sem prefixo/sem codificação em `cache/cache.go:59` e `cache.mdx:196`. Aceite: `rg "unprefixed|no prefix" cache/cache.go site/content/docs/digging-deeper/cache.mdx` retorna 2 hits.
- [ ] **0.5** `internal/tools/release` atualiza a linha "Last release" e "Updated" de `docs/PLAN.md`. Aceite: teste do tool com fixture.
- [ ] **0.6** `docs/architecture.md`: regra para `web.Services` (ARQ-02) e convenção "valores vs ponteiros" (COD-05). Aceite: markdownlint verde; `task lint`.

### Fase 1 — `z`: mapas, normalização, regras de senha (3 dias)

- [ ] **1.1** Dividir `z/tags.go` por kind (`tags_string.go`, `tags_number.go`, `tags_list.go`, `tags_struct.go`) sem mudar comportamento. Aceite: `go test ./z` verde antes e depois; `task api:check` sem diff.
- [ ] **1.2** `needs: 1.1` — Suportar `map[string]V` em `FromTags` com `min`/`max`/`dive` (FUN-01). Aceite: `TestTagRules_Map` (valor tipado, `dive`, limites, panic claro para chave não-string); `TestRegister_MapInput` em `api` com OpenAPI `additionalProperties` tipado.
- [ ] **1.3** `Normalize(v T) (T, error)` em `StructSchema[T]` (FUN-03). Aceite: `TestStruct_Normalize_AppliesTrimDefaultTransform`; godoc de `Check` menciona que não transforma; `task api:check` aprova (adição).
- [ ] **1.4** `HasLower/HasUpper/HasDigit/HasSymbol` com chaves `string.has_*` (FUN-04). Aceite: tabela em `string_test.go`; `localize_test.go` com `pt-BR`; `docs/validation.md` e `validation.mdx:146` listam as regras.
- [ ] **1.5** Release `vX.Y.0` com upgrade note.

### Fase 2 — Cache (1 dia)

- [ ] **2.1** `cache.Add[T]` e `cache.Pull[T]` prefixados e JSON (FUN-02). Aceite: `cachetest.Run` ganha `Add` concorrente (só um vencedor) e `Pull` (segundo retorno `ok=false`); roda em memória, SQLite, Postgres, Redis.
- [ ] **2.2** Doc do site `cache.mdx`: seção "Claims e códigos de uso único" com `Add`; `NewLock` só quando precisa de owner/release.

### Fase 3 — Auth e observabilidade (2 dias)

- [ ] **3.1** Mover a lógica de `LoginThrottleKey`/`LoginAccountThrottleKey` dos scaffolds (`auth-web`, `auth-inertia`) para `auth.ThrottleKeys(emailField string)` no framework, mantendo os scaffolds como chamadores (SEG-02). Aceite: `TestSecurity_A07_LoginThrottle` em `auth`; scaffolds `auth-*` e `api*` compilam nos golden tests de `internal/cli`.
- [ ] **3.2** `Recover` adiciona `slog.Bool("panic", true)`; doc do Telegram mostra `Filter` por esse atributo (SEG-04). Aceite: `TestRecover_LogsPanicAttr`.
- [ ] **3.3** `docs/security.md` e `security/rate-limiting.mdx`: aviso "atrás de proxy, configure `TrustedProxies` ou `ByIP` agrupa todos os clientes" (SEG-03). Aceite: markdownlint; revisão humana.
- [ ] **3.4** `make:seeder --app` (FUN-10). Aceite: golden file em `internal/cli`.

### Fase 4 — Preparação para `v1` (contínuo, `needs:` Fase 1)

- [ ] **4.1** Revisão de pacotes públicos (ARQ-04) registrada em `docs/api/review.md`: para cada diretório da raiz, decisão público/`internal/`/`contrib/`. Aceite: tabela completa; nenhum pacote público sem doc.
- [ ] **4.2** `task release` eleva `require` do core nos módulos aninhados (ARQ-03). Aceite: `grep "vukem v" contrib/*/go.mod drivers/*/go.mod` mostra a versão liberada; `task mod:check` verde.
- [ ] **4.3** Habilitar `gocognit` (limite 30) e corrigir ou justificar violações (COD-03). Aceite: `task lint` verde.
- [ ] **4.4** Teste que compila snippets do site (COD-04). Aceite: `task ci` falha ao introduzir `c.NaoExiste()` num bloco `title="*.go"`.
- [ ] **4.5** Reorganizar arquivos da raiz por ciclo de vida (ARQ-01), sem mudar API. Aceite: `task api:check` sem diff; `app.go` abaixo de 800 linhas.
- [ ] **4.6** `cmp.Or` no lugar de `orDefault` (COD-02); revisar `_ = c.Redirect` (COD-01). Aceite: `task lint`, `task test`.

### Fase 5 — Backlog (sem prazo)

- [ ] **5.1** `factory.PickWeighted` (FUN-11). Aceite: teste de distribuição com seed.
- [ ] **5.2** Decisão em `brainstorming.md` sobre encoder WebP (FUN-08).

## 6. Segurança

Formato: risco → impacto → prova (arquivo:linha) → correção. Cobre entrada, autenticação, autorização, CSRF, upload, configuração e vazamento de informação.

### Entrada e validação

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| Mass assignment por chaves extras | Baixo: `web.Bind` usa `DisallowUnknownFields` (`web/input.go:194`) e `z` emite `unrecognized_keys` em modo `Strict` (`z/composite.go:394,442`) | verificado | Nenhuma. Confirmar que `api.Register` usa `Strict` por padrão (pendência humana) |
| Injeção SQL via nomes de coluna | Baixo: `orm.Update` valida cada chave contra o modelo (`orm/write.go:300-303`); `WhereRaw` exige `?` e conta placeholders (`orm/query.go:230-251`); identificadores sempre com `Dialect.Quote` (`orm/bulk.go:181,200`) | verificado | Nenhuma |
| Filtros/ordenação por parâmetro do cliente | Baixo: `api/query.go:18-68` allowlista `Filters`, `Sorts`, `Includes`, `Fields`; admin allowlista `sortable` (`admin/handlers.go:182,274`) | verificado | Nenhuma |
| Corpo sem limite | Baixo: `BodyLimit(10 MiB)` no router padrão (`http.go:281`), `MaxBytesReader` em presign e tus (`upload/direct.go:158`, `upload/tus.go:384`) | verificado | Nenhuma |
| Regras de senha fracas | Médio-baixo: scaffolds impõem comprimento mínimo; sem composição (FB-4) | `z/string.go` | Fase 1.4, opt-in |

### Autenticação e sessão

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| Força bruta no login | Médio: o framework não traz throttle; scaffolds `auth-web`/`auth-inertia` implementam `LoginThrottleKey` e `api` limita emissão de tokens a 5/min (`scaffolds/api/routes/api.go.tmpl:51`). Um app que não usa scaffold fica sem proteção | `auth/auth.go:164` (só comentário) | Fase 3.1: helper no framework + `TestSecurity_A07_LoginThrottle` |
| Fixação de sessão | Baixo: `Regenerate()` no login (`auth/session.go:141`) | verificado | Nenhuma |
| Cookie inseguro em produção | Baixo: boot falha sem `Secure` (`session/manager.go:103`); `SameSite=None` exige `Secure` (`:107`) | verificado | Nenhuma |
| Reset de senha reutilizável | Baixo: token com SHA-256, TTL e uso único (`scaffolds/auth-core/app/accounts/passwords.go.tmpl:48,82,97,121`) | verificado | Nenhuma |
| Hash fraco | Baixo: argon2id padrão, bcrypt legado com `NeedsRehash` (`auth/hash/hash.go:1-3`) | verificado | Nenhuma |
| JWT `alg=none` / confusão | Baixo: allowlist por chave (`auth/jwt/jwt.go:2-3,35-40`) | verificado | Nenhuma |
| Comparações com timing | Baixo: `hmac.Equal`/`ConstantTimeCompare` em signed, jwt, token, totp, csrf, webhook | `signed/signed.go:180`, `auth/token.go:245`, `csrf/csrf.go:124`, `webhook/standard.go:105` | Nenhuma |

### Autorização

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| Admin sem gate | Baixo: sem `Authorizer` tudo é negado (`admin/admin.go:84-86`) | verificado | Nenhuma |
| Devtools exposto | Baixo: recusa boot em produção (`app.go:955-956`); só loopback com `Host` loopback (`devtools/access.go:19-52`) | verificado | Nenhuma |
| Colisão de namespace no cache (integridade) | Médio: `Store()` cru (`cache/cache.go:59`) permite que uma feature leia/sobrescreva chaves de outra (ex.: contador de rate limit vs código de uso único) se o prefixo manual coincidir | confirmado | Fase 0.4 (doc) + Fase 2.1 (`Add`/`Pull` prefixados) |

### CSRF

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| Clientes sem `Sec-Fetch-Site`/`Origin` | Baixo: fallback por token para requisições com cookie de sessão (`csrf/csrf.go:104-135`), comparação em tempo constante (`:124`) | verificado | Nenhuma |
| Origens confiáveis mal configuradas | Baixo: `AddTrustedOrigin` valida e falha no boot (`csrf/csrf.go:56-58`) | verificado | Nenhuma |

### Upload e arquivos

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| Tipo falso | Baixo: tipo detectado do conteúdo, allowlist de extensão (`upload/upload.go:139,277`) | verificado | Nenhuma |
| Bomba de descompressão | Baixo: `DefaultMaxPixels` 25 MP via `DecodeConfig` (`upload/image.go:17-30`) | verificado | Nenhuma |
| Path traversal | Baixo: `os.Root` + `Clean` (`storage/local.go:18,57`) | verificado | Nenhuma |
| XSS por arquivo servido | Baixo: `nosniff` (`storage/serve.go:57`); `grep svg upload/upload.go` vazio, então SVG não está na allowlist padrão | verificado | Documentar que habilitar SVG exige `Content-Disposition: attachment` ou sanitização (pendência humana) |

### Configuração e segredos

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| `APP_KEY` ausente | Baixo: aviso fora de produção (`app.go:695`); em produção é zero só se `APP_KEY` definido (`app.go:139-141`) | verificado | Nenhuma |
| `APP_DEBUG` em produção | Baixo: boot falha (`app.go:951-952`) | verificado | Nenhuma |
| Proxy confiável | Baixo: `TrustForwardedProto` exige `TrustedProxies` (`middleware/secure.go:79-80`); `c.IP()` usa `RemoteAddr` sem proxies (`web/ctx.go:265-267`) | verificado | Fase 3.3 (doc para `ByIP`) |
| CORS `*` + credenciais | Baixo: recusado (`middleware/cors.go:35-36`) | verificado | Nenhuma |
| Cabeçalhos de segurança | Baixo: CSP estrita com nonce, `DENY`, `strict-origin-when-cross-origin`, Permissions-Policy (`middleware/secure.go:94,127-129`) | verificado | Nenhuma |
| Timeouts do servidor | Baixo: `ReadHeader` 10s, `Read` 30s, `Write` 60s, `MaxHeaderBytes` limitado (`app.go:902,1513-1515`) | verificado | Nenhuma |
| Dependências dos módulos aninhados | Baixo: core resolve por MVS para a versão do app; `govulncheck` em CI | ARQ-03 | Fase 4.2 |

### Vazamento de informação

| Risco | Impacto | Prova | Correção |
| --- | --- | --- | --- |
| Detalhe de 5xx ao cliente | Baixo: só com `Debug` (`web/errors.go:78,96,121-122`); título genérico caso contrário | verificado | Nenhuma |
| Segredos em logs/JSON | Baixo: `config.Secret` redige em `String`, `GoString`, `LogValue`, `MarshalJSON`, `MarshalText` (`config/secret.go:24-37`) | verificado | Nenhuma |
| Query string em logs | Baixo: request logger grava `r.URL.Path`, não a query (`middleware/middleware.go:143`) | verificado | Nenhuma |
| Página de debug | Baixo (dev only): cabeçalhos sensíveis redigidos (`web/debug.go:23-24,104-105`); corpos de formulário não aparecem no template (apenas CSS e cabeçalhos em `web/debug.go`) | parcialmente verificado | SEG-05: confirmar com um teste que campos `password` nunca aparecem no relatório de debug |
| Erro de `Refine` com texto interno | Baixo: fail-closed para `invalid` (`z/schema.go:47-59`) | verificado | Nenhuma |
| Injeção CRLF em e-mail | Baixo: `Subject` e cabeçalhos rejeitam `\r\n` (`mail/mail.go:194,258`) | verificado | Nenhuma |

## 7. Pendências para validação humana

1. **Idioma deste arquivo.** `AGENTS.md` exige inglês em código e docs; este relatório segue a estrutura e o idioma do pedido (pt-BR). Decidir se fica na raiz, vai para `docs/` traduzido, ou vira issues.
2. **`api.Register` e chaves desconhecidas.** Não confirmei se o schema montado por `api.Register` roda em modo `Strict` por padrão (`z/composite.go:442`). Verificar antes de fechar a linha "mass assignment".
3. **SEG-05.** Confirmar com teste que o relatório de debug (`web/debug.go`) nunca renderiza corpo de formulário.
4. **SVG em uploads.** Não há `svg` em `upload/upload.go`; confirmar que nenhum scaffold habilita SVG sem `attachment`.
5. **ARQ-05.** Verificar se `docs/brainstorming.md` registra a decisão "SQLite no core"; se não, registrar.
6. **DOC-06.** Checar se `docs/tutorials/*` citam APIs posteriores a `v0.20.0` enquanto `examples/api` e `examples/saas` exigem `v0.20.0`.
7. **FB-10.** Decisão de política de dependências para um encoder WebP em `contrib/imaging`.
8. **FB-4.** Confirmar o posicionamento: regras de composição como opt-in, mantendo comprimento mínimo como default (alinhado a NIST SP 800-63B).
9. **Grafo de código.** Não rodei `graphify update .` (repo grande, exigiria confirmação); a análise usou `rg`/`sed` e um script de cruzamento site↔API em Python. Se quiser o grafo, rodar depois.
10. **Suite completa.** Rodei `go build ./...` (ok, 9s). Não rodei `task ci` nem `task test:integration` nesta revisão; nenhuma afirmação acima depende disso.
