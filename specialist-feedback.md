# Vukem — consultoria e auditoria independente

Data: 2026-10-25. Base auditada: `main` em `d3c19ee` (release `v0.31.0`, 2026-10-25). Revisão anterior: `vukem-feedback.md` (2026-10-24, base `cf7e016`, `v0.30.0`). Entre as duas bases há 56 commits (`git log --oneline cf7e016..HEAD`).

Escopo: só análise. Nenhum arquivo do framework foi alterado; o único arquivo criado é este.

## 1. Resumo executivo

Veredito: o Vukem está em um estado incomum para um framework com nove dias de repositório público (criado em 2026-10-16): defaults de segurança corretos e testados (270 `TestSecurity_*`, 23 alvos de fuzz), gate de cobertura, apidiff, releases assinadas com SBOM e proveniência, e um Scorecard de 7.1. Os 11 feedbacks e 25 achados da revisão anterior foram, com uma exceção de processo, implementados de verdade: código, teste e documentação, não só commit. O que falta agora é disciplina de operação, não de código: o workflow de fuzz está vermelho há uma semana com duas falhas reais, o módulo de benchmark carrega quatro CVEs do Echo que derrubam o Scorecard, e nenhuma mudança passa por pull request, o que zera `Code-Review`, deixa `CI-Tests` sem dados e trava `Branch-Protection` em 3. Há ainda um ponto de segurança novo no throttle de login (amplificação de memória) e uma inconsistência de política: `SECURITY.md` declara que nenhuma versão `v0.x` recebe correções, mas só existem versões `v0.x`.

| Dimensão | Nota | Justificativa curta |
| --- | --- | --- |
| Arquitetura | 8 | `net/http` puro, sem globais, módulos aninhados independentes, raiz reorganizada por ciclo de vida; o gate apidiff está inerte até `v1.0.0` (seção 4.1) |
| Segurança | 8 | Baseline forte e testado; dois achados médios novos (SEC-01, SEC-02) e um fuzz vermelho ignorado |
| Qualidade de código | 8 | `golangci-lint` limpo com 0 issues, `gocognit` ativo; 26 `//nolint:gocognit` justificados em vez de refatorados |
| Testes | 7 | 270 testes de segurança, race em 3 SOs, fuzz semanal; mas fuzz falhando desde 2026-10-18 e `go test ./...` local estoura 10 min no Windows (`internal/cli`) |
| DX | 7 | CLI com 40+ comandos, erro de comando desconhecido útil, scaffolds compilados em teste; `task test` sem `-short`/`-timeout` e docs de teste citando `e2e/` inexistente |
| Documentação | 7 | Snippets do site checados por tipo (535 blocos, 0 problemas) e tabelas verificadas por teste; mas `snippetcheck` não roda no GitHub Actions, e `PLAN.md`/`testing.md` têm drift |
| Supply chain | 7 | Actions pinadas por SHA, cosign, SBOM, provenance, Dependabot, CodeQL, Scorecard 7.1; vulnerabilidades em `internal/bench`, sem PRs, sem badge Best Practices |

Os 5 problemas mais urgentes:

1. **Fuzz CI vermelho há 7 dias** (`FuzzParseClamReply` e `FuzzParseMetadata` em `upload/`, mais um bug de caminho no upload de artefato do workflow). Sinal de segurança ignorado. Seção 4.4, TST-01.
2. **Scorecard `Vulnerabilities` 5/10** por quatro GHSAs do `labstack/echo v4.16.0` em `internal/bench/go.mod`, módulo que o Dependabot não cobre. Seção 6, SUP-01.
3. **Throttle de login permite amplificação de memória**: a chave do limitador embute o e-mail inteiro (até 10 MiB de corpo) e `ratelimit.Memory` guarda a chave crua. Seção 4.2, SEC-01.
4. **Nenhum fluxo de pull request**: 0 PRs mesclados, `Code-Review` 0, `CI-Tests` sem dados, ruleset `protect-main` só bloqueia force-push e deleção. `CONTRIBUTING.md` descreve um fluxo de PR que o mantenedor não segue. Seção 6, SUP-02.
5. **Gates locais que não existem no CI remoto**: `snippetcheck` só roda em `task ci`; `apicheck -major v1` pula todos os módulos ("no stable v1 tag yet"), então o gate de API é vácuo até `v1.0.0`. Seção 4.1 e 4.5.

## 2. Metodologia e limitações

Verificado:

- Código-fonte completo em `C:\dev\vukem` (`d3c19ee`), com `rg`, `sed`, `git log`, leitura dos arquivos citados. Todas as referências `arquivo:linha` foram lidas nesta base.
- Execução local (Windows 11, Go 1.27.3): `go build ./...` ok; `go vet ./...` ok; `golangci-lint run ./...` 0 issues; `go run ./internal/tools/snippetcheck` 535 blocos, 0 problemas; `go run ./internal/tools/apicheck` ok (mas pula tudo, ver ARQ-01); `go test ./...` passa em todos os pacotes exceto `internal/cli`, que estourou o timeout padrão de 10 min (`FAIL github.com/AlexGalhardo/vukem/internal/cli 600.801s`); `go test -short ./internal/cli/` passa em 2 s. CI remoto em `d3c19ee`: `CI`, `CodeQL`, `Scorecard`, `Site` verdes (`gh run list`); `Fuzz` vermelho em 2026-10-18 e 2026-10-25.
- Scorecard via API (`api.scorecard.dev`, scorecard v5.6.0, resultado de 2026-10-25T11:07Z sobre `d3c19ee`): nota 7.1 e cada check (seção 6).
- GitHub via `gh api`: ruleset `protect-main`, ausência de branch protection clássica, PRs, releases, Dependabot, configurações de segurança do repositório.
- OSV via `api.osv.dev` para as cinco vulnerabilidades listadas pelo Scorecard.
- Site publicado: `https://vukem.dev/docs/introduction` acessado (conteúdo coerente com `site/content/docs`). As demais páginas foram lidas pelo fonte em `site/content/docs` (81 arquivos `.mdx`).
- Uma segunda leitura independente dos pacotes `auth`, `session`, `csrf`, `ratelimit`, `web`, `cache`, `orm`, `middleware`, `upload`, `storage`, `signed` e `crypt` foi feita por um revisor delegado; cada achado dele reportado aqui foi reverificado por mim no código antes de entrar.

Não verificado:

- `task ci:full`, `task test:integration` e os testes de Postgres, MySQL, Redis, Mongo e S3 (sem serviços locais). A evidência é o CI remoto verde.
- `task mod:check` (não executado localmente; CI verde em `d3c19ee`).
- Comportamento em runtime das apps vitrine (Go Quiz, Rifa, Marketplace).
- Se o check `Vulnerabilities` do Scorecard honra `osv-scanner.toml` (seção 6, SUP-01).
- O check `Contributors` do Scorecard não apareceu nas seções recuperadas da API; omitido da tabela.
- `graphify` não foi executado (repositório grande, exigiria confirmação do mantenedor; mesma limitação da revisão anterior).
- A tradução pt-BR das novas mensagens `string.has_*` (o teste `z/localize_test.go:34` cobre a chave, não li o catálogo pt-BR).

## 3. Auditoria do `vukem-feedback.md`

Legenda: **Resolvido**, **Parcial**, **Não resolvido**, **Regressão**, **N/A** (não aplicável ou discordo, com justificativa).

### 3.1 Feedbacks e achados (seções 2 a 4 do arquivo anterior)

| Item | Status | Evidência (base `d3c19ee`) | O que falta |
| --- | --- | --- | --- |
| FB-1 / FUN-01 `map[string]V` em `FromTags` | Resolvido | `z/tags_type.go:107` (`case t.Kind() == reflect.Map` → `mapFromRules`), mensagem do `default` lista os kinds (`:120-121`); `z/tags_map_test.go:10` (`min`, `max`, `dive`, mapa aninhado) e `:70` (`map[int]string` panica com "key must be string"); `api/typed_test.go:409` `TestRegister_MapInput` com `additionalProperties` tipado (`:452`); tabela em `z/tags.go` ("map: min=N, max=N") e `docs/validation.md:78`; commit `efd1335` | Nada |
| FB-2 / FUN-02 / SEG-01 `cache.Add`, `Pull`, `Store()` sem prefixo | Resolvido | `cache/cache.go:83` `Add[T]`, `:95` `Pull[T]` (godoc explicita que não é claim atômico), `:58-60` godoc de `Store` "unprefixed and unencoded"; `cache.mdx:196` idem; `cache/cachetest/cachetest.go:173` `AddConcurrent` (um vencedor), `:200` `Pull`; commits `29e79e1`, `5a43c1a`, `d4e9690` | Nada |
| FB-3 / FUN-03 `Normalize` | Resolvido | `z/composite.go:515` `StructSchema[T].Normalize`; godoc de `Check` (`:509-511`) diz que não transforma; `z/normalize_test.go:10`; commit `e6053e8` | Nada. Não entrou em `Schema[T]` (decisão correta: não quebra implementações externas) |
| FB-4 / FUN-04 `HasLower/HasUpper/HasDigit/HasSymbol` | Resolvido | `z/string.go:254-275`; chaves `string.has_*` em `z/issues.go:204-207`; `z/string_has_test.go:15` (inclui unicode "ção", "ñ", "日本"); `z/localize_test.go:34`; scaffold mantém `Min(12)` como default (`auth-core/app/requests/auth.go.tmpl:31,61`), regras novas são opt-in; commit `0e9791d` | Tradução pt-BR das 4 mensagens não verificada |
| FB-5 / FUN-05 / FUN-11 `PickWeighted` | Resolvido | `factory/faker.go:88-94` `Weighted[T]`, `PickWeighted`; `factory/pickweighted_test.go:5` (distribuição por contagem); commit `ea4da92` | Nada |
| FB-6 / FUN-06 / FUN-10 / DOC-04 `make:seeder --app` | Resolvido | `internal/cli/generate.go:475` flag `--app`, `:499` `ensureAppSeeders`, `:512` `AllApp()`; `internal/cli/cli_test.go:292`; `docs/cli.md:75`; `site/.../cli/overview.mdx:394`, `factories-seeders.mdx:405`; commit `e8eee49` | Nada |
| FB-7 / FUN-07 `NoContent()` sem status | N/A (manter, como a revisão anterior) | `web/response.go:176-177` godoc continua "NoContent writes 204." | Sugestão opcional da revisão anterior ("for another empty status use c.Status(code)") não aplicada; baixo valor, P |
| FB-8 / DOC-07 mensagens i18n | N/A (refutado antes, confirmado) | `z/schema.go` fail-closed `invalid`; `docs/validation.md:177` | Nada |
| FB-9 / SEG-04 `panic=true` no `Recover` | Resolvido | `middleware/middleware.go:57-58` `"panic", true`; `middleware/middleware_test.go:73` `TestRecover_LogsPanicAttr`; `docs/subsystems.md:59-65` exemplo de `Filter` para o Telegram; commit `f87d004` | Nada |
| FB-10 / FUN-08 encoder WebP | N/A (decisão registrada) | `docs/brainstorming.md:158` "WebP output (2026-10-24, FUN-08) … Pending" | Decisão humana sobre dependência externa segue pendente, como previsto |
| FB-11 / FUN-09 helpers JSON em `vukemtest` | N/A (refutado antes, confirmado) | `vukemtest/client.go` `JSON`, `AssertJSON*` | Nada |
| ARQ-01 raiz como composition root | Resolvido com ressalva | `app.go` 731 linhas (era 1.722); novos `options_runtime.go` (15 `With*`), `options_views.go`, `options_routes.go`, `boot.go`, `serve.go`, `validate_config.go`; `.claude/rules/go.md` fixa o arquivo-tema por opção; commit `b5939e0`; build e testes passam | O critério "`task api:check` sem diff" não prova nada hoje: `apicheck -major v1` pula todos os módulos ("no stable v1 tag yet"). Ver ARQ-01 na seção 4.1 |
| ARQ-02 regra para `web.Services` | Resolvido | `docs/architecture.md:125`; commit `43640b4` | Nada |
| ARQ-03 módulos aninhados presos a core antigo | Resolvido | 13 `go.mod` (`contrib/*`, `drivers/*`, `examples/*`) em `v0.31.0` (`rg "vukem v" */*/go.mod`); `internal/tools/release/bump.go` eleva o `require` na release; commits `b698cc8`, `da1908a` | Nada |
| ARQ-04 superfície pública antes de `v1` | Resolvido | `docs/api/review.md` (61 pacotes, 2066 identificadores, decisão por pacote, 140 linhas de tabela); `docs/PLAN.md:579` `B.5.1` marcado `[x]`; commit `1c42ab4` | A tabela "Status" de `PLAN.md` ainda diz "Next task `B.5.1`" (DOC-02 na seção 4.6) |
| ARQ-05 SQLite no core | Resolvido | `docs/brainstorming.md:157` decisão explícita (2026-10-24) | Nada |
| COD-01 `_ = c.Redirect` | Resolvido | `auth/middleware.go:75,237,276`: as três ocorrências têm o comentário "A failed redirect write means the client is gone; nothing to recover."; commit `9fa5205` | Nada |
| COD-02 helpers duplicados | Resolvido | `orDefault` removido em favor de `cmp.Or` (`4a24a2e`; `rg "func orDefault"` vazio). `truncate` segue em 3 lugares com assinaturas diferentes (`admin/values.go:85`, `devtools/devtools.go:437`, `logging/telegram/telegram.go:658`), como a revisão anterior permitiu | Nada |
| COD-03 complexidade e `z/tags.go` | Resolvido com ressalva | `.golangci.yml:59` `gocognit`, `:113-114` `min-complexity: 30`; `z/tags.go` dividido em 6 arquivos, maior com 293 linhas; commits `fb9e023`, `b45be86`, `6922b4b` | 26 `//nolint:gocognit` foram adicionados com justificativa (`app.go:348`, `api/typed.go:114`, `auth/middleware.go:29`, `contrib/graphql/*` …) em vez de refatorar. Aceitável como medida de tendência, mas o linter está medindo pouco. Ver COD-01 na seção 4.3 |
| COD-04 snippets do site não compilam | Parcial | `internal/tools/snippetcheck` (type-check de blocos `title="*.go"`, `nocompile` para fragmentos; `main.go:1-18`); `Taskfile.yml:135-138,176` em `task ci`; local: 535 blocos, 0 problemas; commit `8b8a074` | Não roda no GitHub Actions: `rg snippetcheck .github/workflows/ci.yml` vazio. `AGENTS.md` define o Actions como gate. Adicionar um step no job `lint` (P) |
| COD-05 convenção valores vs ponteiros | Resolvido | `docs/architecture.md:127-130` | Nada |
| SEG-02 throttle de login no framework | Resolvido | `auth/throttle.go:50` `ThrottleKeys`, `:20` `RequestEmail` (JSON via `web.PeekJSON`, forma, query; falha fechado em `unparsedKey` por IP); testes `TestSecurity_A07_LoginThrottle`, `…MatchesHandlerJSON`, `…ParserDifferential` (`auth/throttle*_test.go`); scaffolds `auth-web` e `auth-inertia` usam `ThrottleKeys`; commits `8e6fe4d`, `cd89ae6`, `fe69a79` | Achado novo SEC-01 (amplificação de memória) na seção 4.2 |
| SEG-03 `ByIP` atrás de proxy | Resolvido | `docs/security.md:33` (aviso explícito "one noisy client can lock out the rest"); commit `3cfff73` | Nada |
| SEG-05 página de debug e corpos | Resolvido | `web/debug_body_test.go:13-48` (form e JSON com `password` nunca aparecem no relatório); commit `a4b1c83` | Nada |
| DOC-01 `docs/http.md` lista métodos inexistentes | Resolvido | `docs/http.md:82-90` só métodos reais, aponta `web.Bind[T]` e `auth.User[U]`; `web/docs_test.go:23-33` falha se a doc citar método que não existe em `*Ctx`; commit `f29bb9a` | Nada |
| DOC-02 assinatura de `z.Map` | Resolvido | `docs/validation.md:78` "`z.Map(value)` → `map[string]V`"; commit `4bef333` | Nada |
| DOC-03 tabela de kinds omite `any`/`map[string]any` | Resolvido | `z/tags.go` tabela com `any` e `map`; `z/tags_rules_test.go:166,173-209,239-261` cruza doc comment, `docs/validation.md` e kinds com a implementação | Nada |
| DOC-05 `PLAN.md` desatualizado | Parcial | `internal/tools/release/bump.go:22` atualiza a linha "Updated"; `docs/PLAN.md:22` "Updated 2026-10-24" | A linha "Next task `B.5.1`" está errada (B.5.1 está `[x]` em `:579`); o tool só reescreve "Updated". Ver DOC-02 na seção 4.6 |
| DOC-06 exemplos presos a `v0.20.0` | Resolvido | `examples/api`, `examples/blog`, `examples/saas` em `v0.31.0`; `release/bump.go` mantém | Nada |

### 3.2 Plano de execução (seção 5 do arquivo anterior)

| Fase/item | Status | Evidência |
| --- | --- | --- |
| 0.1 a 0.6 (docs e higiene) | Resolvido | Ver DOC-01, DOC-02, DOC-03, FB-2 (`Store`), DOC-05, ARQ-02/COD-05 acima |
| 1.1 a 1.4 (`z`: split, mapas, `Normalize`, `Has*`) | Resolvido | Ver COD-03, FB-1, FB-3, FB-4 |
| 1.5 release com upgrade note | Resolvido | `CHANGELOG.md` `[0.31.0] - 2026-10-25` lista todos os itens; `docs/upgrade/1.0.md` recebeu a nota sobre `omitempty` (`1645612`) |
| 2.1, 2.2 (cache) | Resolvido | Ver FB-2 |
| 3.1 a 3.4 (auth, Recover, docs de proxy, seeder) | Resolvido | Ver SEG-02, FB-9, SEG-03, FB-6 |
| 4.1 a 4.6 (preparação `v1`) | Resolvido, com 4.4 parcial | 4.1 `docs/api/review.md`; 4.2 `bump.go`; 4.3 `gocognit`; 4.4 `snippetcheck` só local; 4.5 raiz reorganizada; 4.6 `cmp.Or` e comentários nos `Redirect` |
| 5.1 `PickWeighted` | Resolvido | `factory/faker.go:94` |
| 5.2 decisão WebP | Resolvido | `docs/brainstorming.md:158` |

### 3.3 Pendências humanas (seção 7 do arquivo anterior)

| # | Status | Evidência |
| --- | --- | --- |
| 1 Idioma e destino do `vukem-feedback.md` | Não resolvido | Arquivo segue na raiz, em pt-BR, não versionado (`git status`: `?? vukem-feedback.md`). Decisão do mantenedor |
| 2 `api.Register` em modo `Strict`? | Verificado agora: não é estrito, e isso é seguro | `api/typed.go:167-181` monta `c.Input()`/`c.QueryInput()` e chama `web.ParseMap`; `z.FromTags` não chama `Strict()` (`rg "Strict\(\)" api z/tags*.go` vazio). Chaves extras são descartadas porque só os campos declarados são decodificados (parse, don't validate); não há mass assignment. Opcional: documentar que `Strict()` é opt-in |
| 3 SEG-05 | Resolvido | `web/debug_body_test.go` |
| 4 SVG em uploads | Verificado | `rg -i svg upload/*.go` só em teste; os hits em `scaffolds/*2fa*` são QR code TOTP renderizado, não upload. `storage/serve.go` aplica `nosniff` e CSP `sandbox` |
| 5 ARQ-05 em `brainstorming.md` | Resolvido | `docs/brainstorming.md:157` |
| 6 DOC-06 tutoriais vs exemplos | Resolvido na origem | Exemplos em `v0.31.0` |
| 7 Decisão WebP | Pendente por design | `docs/brainstorming.md:158` |
| 8 Posicionamento FB-4 (opt-in) | Resolvido | Regras opt-in; scaffold mantém `Min(12)` |
| 9 Grafo de código | Não executado (mesma limitação) | — |
| 10 Suite completa | Executado nesta auditoria | Seção 2 |

### 3.4 Resumo da conformidade

Dos 32 itens com ação (FB-1 a FB-11, ARQ-01 a 05, COD-01 a 05, SEG-01 a 05, DOC-01 a 06, FUN-10, FUN-11, deduplicados): **26 resolvidos** (3 com ressalva: ARQ-01, COD-03, FB-4 pt-BR não verificado), **2 parciais** (COD-04 `snippetcheck` fora do CI remoto; DOC-05 linha "Next task"), **4 não aplicáveis** confirmados (FB-7, FB-8, FB-10, FB-11), **0 não resolvidos**, **0 regressões**. Das 10 pendências humanas, 1 segue aberta por decisão do mantenedor (idioma/destino do arquivo) e 1 por design (WebP).

Qualidade das correções dos agentes: boa. Cada feature veio com teste de comportamento (não só de existência), godoc, entrada no changelog e página do site. Dois pontos de atenção: (a) o `gocognit` foi "cumprido" com 26 supressões justificadas, não com refatoração; (b) os critérios de aceite que dependiam de `task api:check` eram vácuos, porque o gate pula tudo antes de `v1.0.0`.

## 4. Achados da auditoria independente

Formato: ID, título, severidade, evidência, impacto, recomendação, esforço (P ≤ 1 dia, M 1–3 dias, G > 3 dias).

### 4.1 Arquitetura e design de API

**ARQ-01 — O gate apidiff não protege nada antes de `v1.0.0`.** Médio. Evidência: `.github/workflows/ci.yml:57` roda `go run ./internal/tools/apicheck -major v1`; a saída local é "no stable v1 tag yet (…), skipping" para todos os módulos (`APICHECK_EXIT=0`). `internal/tools/apicheck/main.go:6` já suporta `-major v0`. Impacto: a regra de `AGENTS.md` ("renames keep a deprecated `//go:fix inline` alias") e os critérios de aceite "`task api:check` sem diff" da revisão anterior não são verificados por ninguém; uma quebra acidental entre `v0.31` e `v0.32` passa em silêncio. Recomendação: até a `v1.0.0`, rodar `apicheck -major v0 -report docs/api/apidiff-last.md` no CI em modo informativo (falha só em remoção sem alias) e trocar para `-major v1` no release 1.0. Esforço: P.

**ARQ-02 — `web.Parse` e `web.Bind` não são estritos por padrão, e isso não está escrito.** Baixo. Evidência: `web/input.go:42` `decodeJSON(&body, false)`; `z.FromTags` não aplica `Strict()`; `BindJSON` (`web/input.go:100`) é estrito. Impacto: dois comportamentos para a mesma intenção, sem tabela comparativa; um dev que migra de `BindJSON` para `web.Bind` perde a rejeição de campos desconhecidos sem aviso. Recomendação: uma linha em `docs/validation.md` e `docs/http.md`: "`web.Bind`/`Parse` ignoram chaves extras; use `.Strict()` ou `BindJSON` para rejeitá-las". Esforço: P.

**ARQ-03 — Documentação em duas fontes (`docs/*.md` e `site/content/docs/*.mdx`).** Baixo. Evidência: 81 páginas `.mdx` e 30+ arquivos em `docs/`; testes de sincronização existem só para pontos específicos (`web/docs_test.go`, `z/tags_rules_test.go`, `internal/cli/reference_docs_test.go`, `snippetcheck`). Impacto: a revisão anterior encontrou drift em `docs/http.md` e `docs/validation.md` enquanto o site estava certo; o padrão vai se repetir. Recomendação: declarar em `docs/README.md` qual fonte é canônica por tema (sugestão: `docs/` para contrato e decisões, site para tutorial) e manter o padrão "toda tabela de API em `docs/` tem um teste que a compara com o código". Esforço: M (contínuo).

### 4.2 Segurança

**SEC-01 — Throttle de login permite amplificação de memória no limitador em memória.** Médio. Evidência: `auth/throttle.go:60,73` monta `"login:" + email + ":" + ip` e `"login-account:" + email` sem limitar o tamanho do e-mail; `RequestEmail` (`:20-41`) lê o corpo até o limite da rota (`web/peek.go:24-31`, default `DefaultMaxBody` 10 MiB em `web/input.go:22`); `ratelimit.Memory.Allow` (`ratelimit/ratelimit.go:73-107`) guarda a chave crua em `m.tat` e só poda a cada 1024 chamadas, quando o bucket esvazia. `RATE_LIMIT_STORE` default é `memory` (`http.go:1030`). `ratelimit.Window` faz hash da chave (`ratelimit/window.go:84`), `Memory` não. Impacto: um POST JSON por requisição com `email` de alguns MiB, distinto a cada vez, retém ~2× o tamanho por janela; centenas de requisições chegam a GB em uma instância única. Recomendação: em `ThrottleKeys`, cair em `unparsedKey` quando `len(email) > 254` (RFC 5321) e, em `Memory.Allow`, fazer `sha256` de chaves acima de 128 bytes como já faz `Window`; teste `TestSecurity_A07_LoginThrottleRejectsOversizedEmail` e atualizar a linha em `docs/security.md`. Esforço: P.

**SEC-02 — `cache.Memory` expulsa locks e contadores por LRU, abrindo fail-open silencioso.** Médio-baixo. Evidência: `cache/memory.go:59-81` remove o último elemento da lista ao passar de `max` (default 10000), sem distinguir `lock:*` (`cache/cache.go:217`) ou contadores de outros valores. Impacto: sob inundação de chaves (ex.: `Remember` com chave derivada de input), um lock ativo é expulso e `Add` concede o mesmo lock a outro dono; um contador reinicia. Só afeta quem usa `cache.Memory` para `NewLock` ou rate limit, o que é o caso em dev e em instância única. Recomendação: documentar em `NewLock` e no site ("`Memory` não é adequado para locks sob pressão; use a store compartilhada") e, se quiser garantir, não expulsar entradas com `expires` no futuro cuja chave começa por `lock:`. Esforço: P.

**SEC-03 — `RequestEmail` casa a chave JSON de forma exata; `encoding/json` em struct casa sem diferenciar maiúsculas.** Baixo (condicional). Evidência: `auth/throttle.go:27-29` usa `in[field]` sobre `map[string]any`. O scaffold de login usa `web.Parse` (`auth-web/app/controllers/account/account.go.tmpl:54`), que lê `c.Input()` como mapa exato (`web/input.go:40-46`), então o handler e o throttle veem a mesma chave e o achado **não** se aplica aos scaffolds. Impacto: só um app que decodifique o login com `c.BindJSON(&struct{Email string})` aceitaria `{"EMAIL": "vitima"}` enquanto o throttle cairia no bucket por IP. Recomendação: uma frase na godoc de `ThrottleKeys`: "pair it with web.Parse/Bind, not BindJSON into a struct, or the key may differ in case". Esforço: P.

**SEC-04 — Flash de old input filtra por denylist curta.** Baixo. Evidência: `session/manager.go:452` exclui só chaves que contêm `password` ou iguais a `_token` e `_method`. Impacto: campos como `passwd`, `secret`, `otp`, `cvv` voltam ao formulário via flash (store de sessão ou cookie selado). Recomendação: ampliar a denylist (`pass`, `pwd`, `secret`, `token`, `otp`, `code`, `cvv`, `pin`) e expor uma opção de exclusão por app. Esforço: P.

**SEC-05 — `crypt.Seal`/`Encrypt` panicam com `Encrypter` nil; `ad == nil` permite reuso cruzado de cifrados.** Baixo. Evidência: `crypt/crypt.go:100-102` (`panic`), enquanto `Open` devolve `ErrNoKey`. Impacto: assimetria de API; o `Recover` contém o dano. Recomendação: documentar como contrato de boot (APP_KEY obrigatório) e recomendar `ad` com propósito em todo call site. Esforço: P.

**SEC-06 — Governança: `SECURITY.md` nega suporte a toda versão existente.** Médio. Evidência: `.github/SECURITY.md:10-13` "`v0.x` ❌"; só existem tags `v0.x` (`gh release list`). Impacto: um pesquisador lê que nenhuma versão recebe correção; contradiz a prática (fixes de segurança a cada minor) e o `Security-Policy` do Scorecard avalia só a existência do arquivo, não a coerência. Recomendação: linha "Durante o BETA (`v0.x`), a última minor recebe correções de segurança; versões anteriores não" até a `v1.0.0`. Esforço: P.

Baseline reconfirmado e correto (sem achados): CSRF (`http.CrossOriginProtection` + token em tempo constante), sessão (Secure obrigatório em produção, `Regenerate` no login, idle e absoluto), `IP()` com XFF da direita para a esquerda só de `TrustedProxies`, `PeekJSON` restaura o corpo inclusive em erro, `orm` (identificadores por metadata, `WhereRaw` conta placeholders), `middleware` (CORS recusa `*` com credenciais no boot, HSTS atrás de proxy exige `TrustedProxies`), `upload`/`storage` (`os.Root`, tipo por conteúdo, nome aleatório, `nosniff` + CSP `sandbox`), `signed`/`crypt`/`jwt` (`hmac.Equal`, AEAD com nonce aleatório e key id, allowlist de algoritmos), `auth/token` (custo igualado para id desconhecido), `ratelimit.Middleware` falha fechado com 503 em erro da store.

### 4.3 Qualidade do código Go

**COD-01 — `gocognit` foi ativado e imediatamente silenciado em 26 funções.** Médio. Evidência: `.golangci.yml:113-114` limite 30; `rg -c "nolint:gocognit"` = 26 em código não-teste (`app.go:348`, `api/typed.go:114`, `api/query.go:122`, `api/idempotency.go:77`, `auth/middleware.go:29`, `auth/totp/qr.go:537`, `contrib/graphql/*` 6×, `contrib/stripe/client.go:506` …), mais exclusões por path para `*test/` (`.golangci.yml:164-170`). Impacto: o linter deixa de medir justamente as funções mais complexas; a justificativa "branch order is the security contract" em `auth/middleware.go:29` é razoável, mas 26 é um sinal de que o limite foi escolhido para o código, não o contrário. Recomendação: orçamento decrescente: proibir novos `nolint:gocognit` (regra em `.claude/rules/go.md`), e a cada release retirar dois, começando por `api/query.go:122` e `api/idempotency.go:77` (extrair o estado em tipos). Esforço: M (contínuo).

**COD-02 — `panic` em código de biblioteca fora do boot.** Baixo. Evidência: 50 `panic(` em código não-teste e não-`internal`; os de `z/` e `schedule/` são contratos de boot documentados ("panics on unknown rules … so mistakes surface at boot", `z/tags.go`); `uuid/uuid.go:89`, `social/social.go:187`, `signed/signed.go:93`, `crypt/crypt.go:100` merecem revisão. Impacto: comportamento inconsistente entre pacotes para a mesma classe de erro (configuração ausente). Recomendação: tabela em `docs/architecture.md` "quando panicar": só em construção/registro no boot, nunca em caminho de requisição; revisar os quatro acima. Esforço: P.

**COD-03 — `context.Background()` em caminhos de biblioteca.** Baixo. Evidência: 27 ocorrências fora de testes/internal/examples; a maioria é legítima (shutdown, sinais, `t.Cleanup`, checagens de boot como `flags/http.go:17,41`), mas `web/parse.go:72` (`z` `Parse` sem `Ctx`) e `view/view.go:104,148` (funções de template) criam contexto novo onde um contexto de requisição poderia chegar. Impacto: cancelamento e tracing perdidos nesses pontos; baixo hoje porque nenhum faz I/O. Recomendação: documentar na godoc que essas entradas não propagam contexto, e oferecer a variante com `ctx` se um `Refine` com banco vier a usá-las. Esforço: P.

Positivo: `golangci-lint run ./...` com 0 issues sobre `gofumpt`, `gci`, `golines`, `gosec`, `revive`, `gocritic`, `errcheck` com `check-type-assertions`, `depguard` proibindo `math/rand` em código sensível (`.golangci.yml:109-112`). `template.HTML` aparece 4 vezes em biblioteca, todas com `//nolint:gosec` justificado e escaping feito pelo chamador (`htmx/htmx.go:187`, `inertia/inertia.go:456`, `vite/vite.go:391`, `view/funcs.go:231`).

### 4.4 Testes e CI

**TST-01 — Workflow `Fuzz` vermelho desde 2026-10-18 com duas falhas reais e um bug no upload de artefato.** Alto. Evidência: `gh run list --workflow fuzz.yml` → `failure` em 2026-10-18 e 2026-10-25; log do run `40176342895`: (a) `FuzzParseClamReply`: `parseClamReply(": OK\t") = nil, want an error for a non-OK reply` — `upload/scan.go:175-181` faz `TrimSpace` (que remove `\t`) e aceita prefixo vazio antes de `": OK"`, o oráculo em `upload/fuzz_test.go:88-90` só remove `\x00`, `\r`, `\n` e espaço (não `\t`) e exige sufixo `OK`; (b) `FuzzParseMetadata`: `metadata too large` para uma entrada de 4117 bytes — `parseMetadata` limita a 4 KiB (`upload/tus.go:31,503`) mas `encodeMetadata` (`:531`) não, então o round trip do oráculo falha; (c) `##[error]Invalid pattern '././upload/testdata/fuzz/…'` — o `path` do `upload-artifact` concatena `module` e `pkg` (`.github/workflows/fuzz.yml:31-40` usam `module: .` e `pkg: ./upload`), então o corpus da falha nunca é salvo. Impacto: o sinal de fuzzing (nota 10 no Scorecard) está desligado na prática; ninguém saberia de uma falha real. Recomendação: (a) exigir prefixo não vazio em `parseClamReply` (`clamd` sempre responde `stream: …`) e alinhar o oráculo com `TrimSpace`; (b) limitar o fuzz de metadata ao tamanho que `parseMetadata` aceita ou fazer `encodeMetadata` recusar acima de 4 KiB; (c) `path: ${{ matrix.pkg }}/testdata/fuzz/${{ matrix.target }}/` com `pkg` sem `./`; (d) notificação de falha do `Fuzz` (seção 7). Esforço: P.

**TST-02 — `go test ./...` local no Windows estoura o timeout padrão em `internal/cli`.** Médio (DX, Windows é first-class em `AGENTS.md`). Evidência: `FAIL github.com/AlexGalhardo/vukem/internal/cli 600.801s` com dump de goroutines em `os.removeAllFrom` (`t.Cleanup`); `go test -short ./internal/cli/` passa em 2 s; `internal/cli/cli_test.go:565-570` "e2e tests below compile real programs; they are skipped with -short"; `Taskfile.yml:52-56` (`task test`) não passa `-short` nem `-timeout`. CI remoto no Windows passa (runner mais rápido e menos paralelismo). Impacto: o comando que `AGENTS.md` manda rodar antes de terminar qualquer tarefa falha por tempo numa máquina de desenvolvimento comum. Recomendação: `task test` com `-short -timeout 20m`; `task test:e2e` dedicado para `internal/cli` sem `-short`; `t.Parallel()` controlado nos e2e. Esforço: P.

**TST-03 — `docs/testing.md` descreve um diretório e um comando que não existem.** Baixo. Evidência: `docs/testing.md:24` "End-to-end | `e2e/` | `task test:e2e`"; `ls e2e` falha; `rg "test:e2e" Taskfile.yml` vazio; os e2e vivem em `internal/cli/cli_test.go`. Recomendação: corrigir a tabela, idealmente criando o target de TST-02. Esforço: P.

**TST-04 — Cobertura dos testes de conformidade não exercita o LRU de `cache.Memory` com locks nem chaves grandes no `ratelimit.Memory`.** Baixo. Evidência: `cache/cachetest/cachetest.go` cobre `Add` concorrente e `Pull`, não eviction de `lock:*`; `auth/throttle*_test.go` cobre charset, `+json`, query vs body e trailing data, não e-mail acima de 254 bytes. Recomendação: os dois testes de SEC-01 e SEC-02. Esforço: P.

Positivo: `race` em Linux, macOS e Windows com `coverprofile` e `covergate` 80/90 (`ci.yml:82-103`); `-tags vukem_nosqlite` testado; jobs dedicados para Mongo, OTel, WebSocket, GraphQL com `GOWORK=off`; `gci`, `apidiff`, `openapi`, `markdownlint` como jobs separados; 270 `TestSecurity_*` nomeados pelo controle OWASP; conformance suites compartilhadas (`cachetest`, `queuetest`, `sessiontest`, `dbtest`).

### 4.5 Experiência do desenvolvedor

**DX-01 — `snippetcheck` só existe localmente.** Médio. Evidência: `Taskfile.yml:176,192` (`task ci`, `ci:full`); nenhum step em `.github/workflows/ci.yml`. Impacto: a proteção contra documentação que não compila depende do dev lembrar de `task ci`; o CI remoto, que `AGENTS.md` chama de gate, não a tem. Recomendação: step `go run ./internal/tools/snippetcheck` no job `lint`. Esforço: P.

**DX-02 — Oito worktrees de agentes ficaram em `.claude/worktrees/`.** Baixo. Evidência: `git worktree list` mostra 8 worktrees (`agent-a123e3…`, …), duas `locked`, com 7.102 arquivos `.go` no total; estão em `.gitignore:36`, então não vazam para o repositório, mas inflam `rg`, `find` e IDE, e as duas `locked` não serão removidas por `git worktree prune`. Recomendação: `git worktree remove --force` para cada uma após confirmar que os branches `worktree-agent-*` já foram mesclados (os commits `c0ba9cc`, `e9359ca`, `c9c293a`, `f9b74fa`, `e8d7ad3` são merges deles); adicionar `git worktree prune` ao `task setup`. Esforço: P.

**DX-03 — CLI: comportamento e mensagens bons; `version` imprime `devel` fora de release.** Baixo. Evidência: `go run ./cmd/vukem` lista comandos agrupados; `vukem make:controler Foo` → `usage error: unknown command "make:controler" (app commands need a Vukem project with cmd/app)` + "Run 'vukem list'"; `vukem version` → `vukem devel`. Recomendação: sugerir o comando mais próximo (distância de edição) no erro de comando desconhecido; mostrar o `vcs.revision` de `debug.ReadBuildInfo` em `devel`. Esforço: P.

Positivo: 40+ comandos com `install:*` compostos (auth, api, billing, admin, flags, 2FA, social, Inertia React/Vue/Svelte, templ), scaffolds compilados em teste (`internal/cli/cli_test.go`), `reference_docs_test.go` falha quando um comando ou flag falta na referência do site (`c092c0e`), `boost:install` para agentes, `lefthook` com `fmt`, lint incremental, `go mod tidy -diff`, markdownlint e `commitlint` próprio.

### 4.6 Documentação

**DOC-01 — Governança de suporte incoerente em `SECURITY.md`.** Ver SEC-06.

**DOC-02 — `PLAN.md` com "Next task" obsoleto.** Baixo. Evidência: `docs/PLAN.md:21` "Next task `B.5.1` (public API review); B.5.2, B.5.4 and B.5.6 have work in progress" vs `:579,581,587,590` todos `[x]`; só `S.3.1`, `S.3.2` e os critérios de saída estão abertos (`:623-634`). O `release` tool reescreve só a linha "Updated" (`internal/tools/release/bump.go:22-26`). Recomendação: o tool deriva "Next task" do primeiro `- [ ]` com ID. Esforço: P.

**DOC-03 — `CONTRIBUTING.md` descreve um fluxo de PR que o próprio projeto não pratica.** Baixo (vira Médio se chegar contribuidor externo). Evidência: `.github/CONTRIBUTING.md:18-24` "Open a pull request … squash-merge"; `gh pr list --state all` mostra 1 PR ("update stuff", não mesclado); 0 PRs mesclados segundo o Scorecard. Recomendação: ou o mantenedor adota PRs (seção 6), ou o documento diz explicitamente que o mantenedor faz push direto e PRs são para externos. Esforço: P.

Positivo: `docs/security.md` como contrato com um `TestSecurity_*` por linha; `docs/security-review.md` (OWASP Top 10:2025 e ASVS) e `docs/api/review.md` como registros de revisão; `docs/brainstorming.md` com decisões datadas; `docs/upgrade/` por versão; site com 81 páginas, snippets type-checked; `CHANGELOG.md` gerado por git-cliff no formato Keep a Changelog com links de commit.

### 4.7 Performance e recursos

**PERF-01 — Benchmarks existem, mas o módulo que compara com Echo e chi é também o que traz as vulnerabilidades.** Baixo (o impacto real está em SUP-01). Evidência: `internal/bench/go.mod:7-8` (`chi v5.5.0`, `echo v4.16.0`); `.github/workflows/bench.yml:32-35` roda `./router ./web ./orm` e o módulo de comparação; 17 `Benchmark*` no core. Recomendação: manter o módulo, mas cobri-lo no Dependabot e no `govulncheck` (SUP-01). Esforço: P.

Positivo: router sobre `http.ServeMux` (padrões Go 1.22, `router/router.go:1,50`); `DefaultMaxHeaderBytes` 64 KiB e timeouts explícitos (`serve.go:166-176`); `BodyLimit` 10 MiB por default com checagem preguiçosa de `Content-Length` (`web/input.go:150-175`); upload com `DecodeConfig` antes de decodificar imagem; `docs/performance.md` com baseline (`B.5.3` concluído).

### 4.8 Manutenibilidade e governança

Ver SEC-06/DOC-01 (suporte), DOC-03 (fluxo de PR), SUP-* na seção 6. Positivo: MIT, SemVer com tags por módulo (`drivers/postgres/v0.5.0`), Conventional Commits validados por hook, `CODEOWNERS`, templates de issue e PR, `CODE_OF_CONDUCT.md`, release automatizada (`task release` → changelog, tag, GoReleaser, cosign keyless, SBOM syft, provenance). Risco estrutural: um único mantenedor (`CODEOWNERS: * @AlexGalhardo`), 0 forks, 0 stars nove dias após a criação; isso não é defeito, mas define o que o Scorecard pode medir (seção 6).

## 5. Pontos fortes

Preservar sem discussão:

- **Contrato de segurança testável**: cada default em `docs/security.md` tem um `TestSecurity_<Axx>_*`; 270 hoje. É o melhor mecanismo do projeto e o que torna as afirmações da documentação verificáveis.
- **Fail closed em toda a cadeia**: `ratelimit.Middleware` 503 em erro da store; `auth.Required` nega em erro não-`ErrUnauthenticated`; `unparsedKey` por IP no throttle; `Refine` genérico vira `invalid`; boot recusa `APP_DEBUG`, cookies sem `Secure` e `TrustForwardedProto` sem `TrustedProxies`.
- **Stdlib-first de verdade**: `go.mod` do core com três dependências diretas (`go-cmp`, `x/crypto`, `modernc.org/sqlite`); `net/http.ServeMux`, `html/template`, `log/slog`, `os.Root`, `crypto/rand`.
- **Supply chain acima da média**: 63 de 63 Actions pinadas por SHA com comentário de versão, `permissions: contents: read` em todos os workflows, cosign keyless + SBOM + attestation em cada release, Dependabot agrupado, CodeQL, govulncheck semanal e no `pre-push`, Scorecard publicado.
- **Documentação verificada por código**: `web/docs_test.go`, `z/tags_rules_test.go`, `internal/cli/reference_docs_test.go`, `snippetcheck`. Expandir esse padrão é mais valioso do que qualquer revisão manual.
- **Descrições como valores imutáveis** (`z`, `orm.Query[T]`, `factory.Factory[T]`) e registradores como ponteiros, agora escrito em `docs/architecture.md:127-130`.
- **Conformance suites** (`cachetest`, `queuetest`, `sessiontest`, `dbtest`) rodando o mesmo cenário em memória, SQLite, Postgres, Redis: é o que permitiu adicionar `cache.Add` com confiança em todos os backends.

## 6. Plano de ação do Scorecard

Resultado atual: **7.1** (scorecard v5.6.0, commit `d3c19ee`, 2026-10-25T11:07Z). Checks com peso: os de maior peso são `Binary-Artifacts`, `Branch-Protection`, `Code-Review`, `Dangerous-Workflow`, `Maintained`, `Signed-Releases`, `Token-Permissions`, `Vulnerabilities` e `Webhooks` (alto); `CI-Tests` com `-1` não entra na média.

| Check | Nota | Meta | Por que perdeu pontos | Ação | Quem consegue |
| --- | --- | --- | --- | --- | --- |
| Binary-Artifacts | 10 | 10 | — | Manter | Solo |
| Branch-Protection | 3 | 6 (8 com revisor) | Ruleset `protect-main` só tem `non_fast_forward` e `deletion` (`gh api rulesets/25297704`); "no status checks", "PRs are not required" | Adicionar ao ruleset: `pull_request` (0 aprovações, `dismiss_stale_reviews_on_push`), `required_status_checks` com os jobs `lint`, `apidiff`, `openapi`, `markdown`, `Test (ubuntu-latest, Go stable)`, `Test (windows-latest, Go stable)`, `Test (macos-latest, Go stable)`, `required_linear_history`; `bypass_actors` vazio (já está). 8+ exige 1 aprovação, impossível sozinho | Solo até 6 |
| CI-Tests | -1 | 10 | "no pull request found": o check só avalia PRs mesclados | Passar a mesclar via PR (mesmo próprio): cada PR com os checks acima verdes conta | Solo |
| CII-Best-Practices | 0 | 5 (passing); 7 (silver) | Sem badge em bestpractices.dev | Registrar o projeto; o nível *passing* exige itens que o Vukem já cumpre (licença, changelog, testes automatizados, política de segurança, build reproduzível, HTTPS). Colocar o badge no README | Solo (meio dia) |
| Code-Review | 0 | 0 realista; 10 exige revisor humano | "Found 0/30 approved changesets" | Sem segundo humano não há como pontuar: o autor não pode aprovar o próprio PR e revisões de bot não contam. Alternativas honestas: (a) convidar um co-mantenedor que revise só PRs de release e de segurança; (b) aceitar o 0 e documentar em `SECURITY.md`/README que o projeto é solo e compensa com `TestSecurity_*`, fuzz, CodeQL e revisões de agente registradas (`docs/security-review.md`). Não criar segunda conta para aprovar: é fraude de métrica e viola os termos do GitHub | Time |
| Dangerous-Workflow | 10 | 10 | — | Manter (`pull_request_target` nunca; não usar `${{ github.event.*.title }}` em `run:`) | Solo |
| Dependency-Update-Tool | 10 | 10 | — | Manter; adicionar `/internal/bench` aos `directories` do `gomod` em `.github/dependabot.yml` (hoje faltando) | Solo |
| Fuzzing | 10 | 10 | — | Manter, e consertar o workflow (TST-01), senão a nota mente | Solo |
| License | 10 | 10 | — | Manter | Solo |
| Maintained | 0 | 10 | "project was created within the last 90 days" (criado 2026-10-16) | Só o tempo resolve: a partir de 2027-01-14, com ≥ 1 commit/semana ou issues respondidas, sobe para 10. Não há atalho | Solo |
| Packaging | 10 | 10 | — | Manter | Solo |
| Pinned-Dependencies | 9 | 10 | `docs/media/Dockerfile:2` `FROM ghcr.io/charmbracelet/vhs:v0.11.0` sem digest | `FROM ghcr.io/charmbracelet/vhs:v0.11.0@sha256:9d5fc3dc0c160b0fb1d2212baff07e6bdf3fa9438c504a3237484567302fcf93` (digest do próprio relatório; confirmar com `docker buildx imagetools inspect`). Dependabot `docker` para `/docs/media` | Solo |
| SAST | 10 | 10 | — (aviso "no pull requests merged") | Manter CodeQL; com PRs o aviso some | Solo |
| Security-Policy | 10 | 10 | — | Manter; corrigir a tabela de suporte (SEC-06) | Solo |
| Signed-Releases | 10 | 10 | — | Manter cosign + provenance | Solo |
| Token-Permissions | 10 | 10 | — | Manter `permissions: contents: read` no topo e elevação só por job | Solo |
| Vulnerabilities | 5 | 9 (10 se o x/crypto for resolvido) | 5 vulnerabilidades: `GO-2026-5932` (`golang.org/x/crypto/openpgp`, sem fix, não importado; já em `osv-scanner.toml`) e 4 GHSAs do `labstack/echo/v4 v4.16.0` em `internal/bench/go.mod` (`GHSA-h7wr-284f-5qvc` corrigida em `v4.16.1`; as outras três presumivelmente na mesma release, **não verificado**) | `cd internal/bench && go get github.com/labstack/echo/v4@latest && go mod tidy`; incluir `internal/bench` no Dependabot e no job `govulncheck`. Para `GO-2026-5932`: não há fix; Scorecard não parece honrar `osv-scanner.toml` (**não verificado**). Alternativa é a diretiva `godebug`/`exclude` não aplicável; aceitar 9 ou abrir issue no Scorecard pedindo suporte ao arquivo de ignore | Solo |

Projeção honesta (estimativa, não verificada): com Vulnerabilities 9, Pinned 10, CII 5, Branch-Protection 6 e CI-Tests 10, a nota deve ficar entre 8.0 e 8.5; `Maintained` 10 em meados de janeiro de 2027 leva para perto de 9. `Code-Review` 0 é o teto estrutural de um projeto solo.

### Roadmap em fases

**Fase A — ganhos rápidos (esta semana, ~1 dia):**

1. `internal/bench`: `go get github.com/labstack/echo/v4@latest` (≥ `v4.16.1`), `go mod tidy`; adicionar `"/internal/bench"` em `.github/dependabot.yml` (`gomod.directories`) e à matriz `module` de `.github/workflows/vuln.yml`.
2. `docs/media/Dockerfile:2`: pin por digest; novo bloco `package-ecosystem: docker`, `directory: /docs/media` no Dependabot.
3. Ruleset `protect-main` (Settings → Rules → `protect-main`): habilitar *Require a pull request before merging* (0 aprovações), *Require status checks to pass* com os jobs do `ci.yml` e *Require linear history*. Equivalente via API:

   ```bash
   gh api -X PUT repos/AlexGalhardo/vukem/rulesets/25297704 --input ruleset.json
   ```

   com `rules` contendo `non_fast_forward`, `deletion`, `required_linear_history`, `pull_request` (`required_approving_review_count: 0`, `dismiss_stale_reviews_on_push: true`) e `required_status_checks` (`strict_required_status_checks_policy: true`, contexts dos jobs).

4. Passar a trabalhar em branch + PR + squash (o `pr-title.yml` já valida o título). O próprio `task release` pode abrir o PR de release com `gh pr create`.
5. Consertar `Fuzz` (TST-01) e adicionar `snippetcheck` ao `ci.yml` (DX-01).
6. `SECURITY.md`: linha de suporte para `v0.x` durante o BETA (SEC-06).

**Fase B — médio prazo (próximas 2 a 4 semanas):**

1. Registrar em `https://www.bestpractices.dev` e preencher o questionário *passing*; badge no README. Itens que exigem escrita: documentação de "como reportar bug", "processo de release", "testes para cada correção" (já existem; apontar as URLs).
2. `apicheck -major v0` informativo no CI (ARQ-01).
3. SEC-01, SEC-02 com seus `TestSecurity_*`.
4. Notificação de falha de workflow agendado (`Fuzz`, `vuln`, `Scorecard`): job final `if: failure()` que abre/atualiza uma issue com `gh issue create --label ci-failure` (token `issues: write` só nesse job), ou e-mail via configuração de notificações do GitHub Actions.
5. `task test` com `-short -timeout 20m` e `task test:e2e` (TST-02, TST-03).

**Fase C — longo prazo (até `v1.0.0` e depois):**

1. Co-mantenedor ou revisor convidado para PRs de release e de segurança (único caminho para `Code-Review` e `Branch-Protection` acima de 6).
2. `Maintained` sobe sozinho após 2027-01-14 com atividade semanal.
3. Nível *silver* do Best Practices (exige, entre outros, assinatura de releases — já feito — e análise dinâmica — fuzz já cobre).
4. Na `v1.0.0`: `apicheck -major v1` passa a bloquear; `release/1.x` conforme `SECURITY.md`.

## 7. Workflow de longo prazo

### Rotina de release (a cada grupo de tarefas do `PLAN.md`)

1. Branch `release/vX.Y.Z` → `task ci` local → push → PR → esperar `lint`, `apidiff`, `openapi`, `markdown`, `Test ×3`, `snippets` verdes → squash.
2. `task release -- vX.Y.Z` (changelog, tag, nested bumps, PLAN status).
3. Pós-release (15 min): `gh run list --workflow release.yml` verde; `cosign verify-blob` do `checksums.txt` publicado; abrir `https://api.scorecard.dev/projects/github.com/AlexGalhardo/vukem` e anotar a nota na issue de acompanhamento; bump das apps vitrine.

### Rotina de dependências (semanal, segunda-feira)

1. Dependabot roda semanalmente; `vuln.yml` roda às 06:17 UTC de segunda. Mesclar os PRs agrupados (`go-deps`, `actions`, `site-deps`, e o novo `bench`) depois do CI verde; nunca `@latest` sem SHA em Actions.
2. `osv-scanner.toml`: revisar a cada upgrade de `golang.org/x/crypto`; remover `GO-2026-5932` se um fix sair.
3. Trimestral: `go mod graph | grep -v AlexGalhardo | wc -l` por módulo; justificar qualquer dependência nova em `docs/architecture.md#dependency-policy`.

### Rotina de revisão de segurança

1. Toda alteração em `auth`, `session`, `csrf`, `crypt`, `signed`, `storage`, `upload`, `router`, `orm`, `middleware`: atualizar a linha em `docs/security.md` e o `TestSecurity_*` no mesmo PR (regra já em `.claude/rules/secure-coding.md`).
2. `Fuzz` semanal deve estar verde; falha abre issue automaticamente (Fase B.4) e bloqueia o próximo release até triagem.
3. Trimestral: reexecutar `docs/security-review.md` contra OWASP Top 10 e ASVS; registrar a data na própria página.
4. A cada release de segurança: entrada **Security** no changelog, opt-out `Insecure*` quando razoável, exceção em `apicheck` se quebrar API (`docs/stability.md`).

### Checklist de PR (substituir `.github/pull_request_template.md`)

- [ ] Título Conventional Commit; corpo com `Refs: <task>`.
- [ ] `task ci` verde localmente (`lint`, `vet`, `mod:check`, `snippets:check`, `test -short`).
- [ ] Toda mudança de default de segurança tem linha em `docs/security.md` e `TestSecurity_*`.
- [ ] API exportada nova tem godoc, página no site e linha em `docs/api/review.md`; rename mantém alias `//go:fix inline`.
- [ ] Nenhum `//nolint` novo sem justificativa de uma linha; nenhum `//nolint:gocognit` novo.
- [ ] Nenhuma dependência nova sem decisão em `docs/brainstorming.md`.
- [ ] Workflows: Actions pinadas por SHA com comentário de versão; `permissions` mínimas.
- [ ] Changelog gerado pelo tool, nunca editado à mão em seção publicada.

### Métricas para acompanhar (issue fixa "Scorecard tracking", atualizada a cada release)

| Métrica | Fonte | Meta |
| --- | --- | --- |
| Nota Scorecard e nota por check | `api.scorecard.dev` | ≥ 8.5 até `v1.0.0`; nenhum check cai entre releases |
| Vulnerabilidades abertas (OSV) | `govulncheck` + Scorecard | 0 corrigíveis; só `GO-2026-5932` enquanto não houver fix |
| Workflows agendados verdes | `gh run list --workflow fuzz.yml/vuln.yml/scorecard.yml` | 100% nas últimas 4 semanas |
| Cobertura | `covergate` | ≥ 80% geral, ≥ 90% pacotes estritos; sem queda entre releases |
| `//nolint:gocognit` em código não-teste | `rg -c` | 26 hoje → 0 na `v1.0.0` |
| PRs de Dependabot abertos há > 7 dias | `gh pr list --author app/dependabot` | 0 |
| Tempo entre falha de `Fuzz` e correção | issues `ci-failure` | < 7 dias |
| Divergência doc ↔ código | `web/docs_test.go`, `tags_rules_test.go`, `reference_docs_test.go`, `snippetcheck` no CI | 0 falhas; cada tabela nova de API ganha um teste |

## 8. Roadmap priorizado (impacto vs. esforço)

| # | Ação | Impacto | Esforço | Refs |
| --- | --- | --- | --- | --- |
| 1 | Consertar `Fuzz`: `parseClamReply`, oráculo de metadata, `path` do artefato | Alto (sinal de segurança desligado) | P | TST-01 |
| 2 | Bump `echo` em `internal/bench`; Dependabot e `govulncheck` cobrindo o módulo | Alto (Scorecard 5 → 9 em `Vulnerabilities`) | P | SUP-01 |
| 3 | Ruleset com PR obrigatório + status checks + histórico linear; trabalhar via PR | Alto (`Branch-Protection` 3 → 6, `CI-Tests` -1 → 10) | P | SUP-02 |
| 4 | `snippetcheck` no `ci.yml` | Médio | P | DX-01 |
| 5 | SEC-01: limite de tamanho do e-mail no throttle; hash em `ratelimit.Memory` | Médio | P | SEC-01 |
| 6 | `SECURITY.md` com suporte explícito para `v0.x` no BETA | Médio | P | SEC-06 |
| 7 | Pin por digest em `docs/media/Dockerfile` + Dependabot `docker` | Médio (`Pinned` 9 → 10) | P | SUP-03 |
| 8 | `task test -short -timeout 20m` + `task test:e2e`; corrigir `docs/testing.md` | Médio (DX Windows) | P | TST-02, TST-03 |
| 9 | Badge OpenSSF Best Practices (*passing*) | Médio (`CII` 0 → 5) | P–M | SUP-04 |
| 10 | `apicheck -major v0` informativo no CI | Médio | P | ARQ-01 |
| 11 | Notificação automática de falha em workflows agendados | Médio | P | Fase B.4 |
| 12 | SEC-02: doc e proteção de `lock:*` no `cache.Memory` | Médio-baixo | P | SEC-02 |
| 13 | Orçamento decrescente de `//nolint:gocognit` | Médio (contínuo) | M | COD-01 |
| 14 | `PLAN.md` "Next task" derivado; `CONTRIBUTING.md` coerente com o fluxo | Baixo | P | DOC-02, DOC-03 |
| 15 | Remover worktrees de agentes; `git worktree prune` no `task setup` | Baixo | P | DX-02 |
| 16 | SEC-03, SEC-04, SEC-05 (godoc do throttle, denylist do flash, `crypt` nil) | Baixo | P | — |
| 17 | ARQ-02 (documentar `Strict` opt-in), COD-02 (tabela "quando panicar"), COD-03 (godoc das entradas sem contexto) | Baixo | P | — |
| 18 | Fonte canônica por tema entre `docs/` e site | Baixo (contínuo) | M | ARQ-03 |
| 19 | Co-mantenedor/revisor para PRs de release e segurança | Alto no Scorecard, depende de pessoa | G | Fase C |
| 20 | Decisão WebP e destino do `vukem-feedback.md` | Baixo | — | FB-10, pendência 1 |

## 9. Próximos passos imediatos (próxima semana)

1. **Dia 1**: corrigir os dois alvos de fuzz e o `path` do artefato em `fuzz.yml`; rodar `go test -run Fuzz -fuzz=FuzzParseClamReply -fuzztime=60s ./upload` e o mesmo para `FuzzParseMetadata` localmente; disparar o workflow com `gh workflow run fuzz.yml` e confirmar verde.
2. **Dia 1**: `cd internal/bench && go get github.com/labstack/echo/v4@latest && go mod tidy`; adicionar `/internal/bench` ao Dependabot (`gomod`) e à matriz de `vuln.yml`; `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` dentro de `internal/bench`.
3. **Dia 1**: `docs/media/Dockerfile` com digest; bloco `docker` no Dependabot.
4. **Dia 2**: atualizar o ruleset `protect-main` (PR obrigatório, status checks dos jobs do `ci.yml`, histórico linear). A partir daqui, todo commit em `main` vem de um PR; o primeiro PR pode ser exatamente o deste checklist.
5. **Dia 2**: step `snippetcheck` no job `lint` de `ci.yml`; `task test` com `-short -timeout 20m` e novo `task test:e2e`; corrigir `docs/testing.md:24`.
6. **Dia 3**: SEC-01 (`ThrottleKeys` recusa e-mail > 254 bytes; `ratelimit.Memory` faz hash de chave longa) com `TestSecurity_A07_LoginThrottleRejectsOversizedEmail` e linha em `docs/security.md`; SEC-02 documentado em `NewLock` e no site.
7. **Dia 3**: `SECURITY.md` com a política de suporte do BETA; `PLAN.md` linha "Next task"; `CONTRIBUTING.md` alinhado ao fluxo de PR.
8. **Dia 4**: registrar o projeto em bestpractices.dev e preencher o nível *passing*; badge no README.
9. **Dia 4**: job de notificação de falha para `fuzz.yml`, `vuln.yml` e `scorecard.yml` (issue automática com label `ci-failure`).
10. **Dia 5**: release `v0.32.0` via PR; após o merge, verificar `api.scorecard.dev` e registrar a nota na issue "Scorecard tracking"; remover os 8 worktrees em `.claude/worktrees/`.
