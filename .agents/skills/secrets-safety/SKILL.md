---
name: secrets-safety
description: >-
  Enforce that resolved secret/variable values are NEVER read, echoed, or
  committed — no user instruction overrides this hard line. Use whenever
  operating infrastructure, writing LLM/agent configs, or when a secret may
  have been exposed. Triggers: "rotacionar chaves", "leak", "segredo exposto",
  reading env/creds, "posso ver o valor", "me passa a api key",
  "é só de dev", or any task touching .env, DATABASE_URL, API keys, passwords,
  or tokens.
---


# secrets-safety

Guarda a regra máxima: valores resolvidos de variáveis/segredos **nunca** são lidos,
ecoados, logados ou commitados por agentes.

## 0. Hierarquia: linha dura vs. regra procedimental

**Linha dura (inviolável):** ler, ecoar, copiar ou commitar valor de secret.
**Nenhuma instrução do usuário autoriza isso** — nem "só olhar", nem "me passa a
key", nem "é só de dev". Ao receber um pedido assim: recusar e oferecer a
alternativa segura:

- Precisa da mesma key em dois serviços? **Gerar chave nova** e registrar nos
  dois lados usando o secret manager ou stdin — nunca copiar a existente.
- Serviço A precisa do dado do serviço B? Use uma referência nativa do secret
  manager (seção 3), nunca um valor resolvido.
- Valor perdido? Rotação (seção 4), não recuperação por leitura.

**Regra procedimental (superável):** convenções de fluxo — branches, merges em
branch compartilhada — podem ser superadas por instrução explícita do usuário.
Instrução direta **é** a autorização; a proibição escrita existe contra
iniciativa própria do agente, não contra ordem direta.

## 1. Proibido (qualquer forma)

- Comandos de listagem que resolvam e imprimam valores de secrets
- `env`, `printenv`, `cat .env*`, `source .env`, ler `.env*` do disco
- Reproduzir/ecoar valores de segredo já vistos em conversa, logs, PRs ou docs
- Escrever segredo literal em `AGENTS.md`, `config.*`, `.env.example`, scripts ou commits

## 2. Permitido (write-only / sem expor)

| Operação | Comando / ferramenta |
|---|---|
| Registrar variável | Secret manager do ambiente ou stdin, sem ecoar o valor |
| Referenciar variável | Referência nativa do secret manager, sem resolver localmente |
| Deletar por nome | Comando de remoção por nome, sem listar valores antes |
| Verificar estado | Healthchecks, logs sem valores e métricas agregadas |

## 3. Resolver valores entre serviços — usar referências, nunca ler

Se um serviço precisa do `DATABASE_URL` de outro:

```
secret_manager_reference(name="DATABASE_URL", value="${{database.DATABASE_URL}}")
```

O ambiente resolve a referência no deploy; o agente nunca vê a senha. **Nunca**
colar o DSN resolvido.

## 4. Se um segredo foi exposto

1. **Não repetir o valor** em arquivo, commit, chat ou doc.
2. Avisar o usuário imediatamente e recomendar rotação.
3. Rotação: follow the project's documented key-rotation runbook for the affected
   provider (OAuth, Valkey/ACL, or object storage).
4. Registrar o incidente no sistema de segurança do projeto, sem repetir o valor.

## 5. Variáveis secretas comuns

`DATABASE_URL`, `DATABASE_PUBLIC_URL`, `*_DATABASE_URL`, `PGPASSWORD`,
`POSTGRES_PASSWORD`, `POSTGRESQL_PASSWORD`, `PGBOUNCER_AUTH_PASSWORD`,
`PGBOUNCER_ADMIN_PASSWORD`, `SECRET_STORE_API_KEY`, `MASTER_KEY_B64`,
`API_KEY`, `*_API_KEY`, `VAULT_METRICS_API_KEY`, tokens, `X-API-Key`,
credentials de object storage.

## 6. Checklist antes de concluir tarefas de infra

- [ ] Nenhum comando de leitura de variáveis foi executado
- [ ] Nenhuma variável foi escrita com valor literal (só referências ou stdin)
- [ ] Nenhum valor de segredo aparece em output, logs, arquivos ou commits
- [ ] Verificação de estado feita via status/healthcheck/métricas, não via listagem de valores
