# Busca e planejamento no Jira

- Buscas usam `page_token` no Cloud e `start_at` no Data Center; board e sprint usam `start_at`. O próximo índice considera a quantidade efetivamente recebida.
- A busca entrega texto e `structuredContent` na mesma chamada. Campos solicitados são preservados como JSON, inclusive `null`.
- Cada chamada usa seu próprio contexto, compartilhando o cliente HTTP. Apenas leituras podem repetir uma vez por `429`, com `Retry-After` válido de até cinco segundos.
- A auditoria guarda categorias fixas de falha, sem argumentos, mensagens do Jira ou conteúdo de anexos.
- Upload recebe bytes Base64, sem acesso ao filesystem do servidor, com o mesmo limite de 15 MiB do download.
- O planejamento move uma issue por chamada. Sucesso na escrita e falha na confirmação devem ser distinguíveis para não incentivar repetição da escrita.
- O filtro de histórico é aplicado aos itens da página; a continuação considera o histórico não filtrado, mesmo se a página filtrada estiver vazia.
