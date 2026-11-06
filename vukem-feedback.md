# vukem feedback from HexaScore

Open queue only. Fixed, withdrawn and wontfix entries are removed; git history keeps them. Do not reuse removed ids.

One entry per finding. Update `status` when it changes. Cite <https://vukem.dev/docs> (page URL + heading). The local `vukem/docs` tree is cited only when the site has no page, tagged `[SITE PAGE MISSING]`.

Severity: `critical` (blocks a roadmap task), `high` (workaround costs more than a day), `medium` (workaround under a day), `low` (DX or docs).
Status: `open`, `reported` (issue or PR link), `fixed in vX.Y.Z`, `wontfix`, `withdrawn`.

Every entry uses this shape. Fixed entries are deleted from the queue; the shape stays.

```md
## <id>: <title>

- Severity: low
- Status: open
- Date: YYYY-MM-DD
- Docs: https://vukem.dev/docs/<page> (heading)
- Finding: what was observed
- Decision: the fix this entry chose, including what it rejected
- Acceptance: the check that closes it
```

## O que não deve ser atribuído ao Vukem

- A falta inicial de Go/Bun corretos no PATH e o disco limitado do executor eram
  condições do ambiente, não falhas demonstradas do runtime do framework.
- A interpretação de XML pelo renderer de partituras, a precisão dos prompts
  ElevenLabs e as restrições de distribuição de samples pertencem às integrações
  externas e às escolhas da aplicação.
- O backup e sua assertion POSIX são código local. A correção deve acontecer nele
  antes de apresentar isso como regressão do framework.
- DNS em cache, domínio/TXT cadastrado separadamente e disponibilidade de volume
  no pre-deploy são comportamentos da infraestrutura que pedem receitas claras.
- O conflito entre alterações simultâneas na `main` é um assunto de fluxo Git;
  integrar mudanças sem sobrescrevê-las foi responsabilidade desta aplicação.
- Leitura de partituras, DSP, teoria musical, regras de progresso e revisão do
  conteúdo não devem entrar no core HTTP do Vukem apenas por existirem aqui.

Não foram avaliados com profundidade WebSockets, filas distribuídas, múltiplas
réplicas com escrita concorrente, outras bases SQL ou recuperação de desastre
com troca real de tráfego. Este relato não certifica nem critica esses cenários.
