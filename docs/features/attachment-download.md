# Download de anexos via MCP

`jira_download_attachment` resolve o anexo dentro da issue informada, usando
exatamente um identificador (`attachment_id` ou `filename`). Se houver nomes
duplicados, o cliente deve usar o ID. `jira_get_attachment` continua sendo a
ferramenta de prévia de imagem.

A nova ferramenta devolve os bytes originais em um recurso MCP embutido
(`resource.blob`, Base64), com nome, MIME e tamanho. Assim funciona tanto em
stdio quanto em HTTP, sem expor credenciais, publicar links temporários ou
escrever no disco do servidor. Cabe ao cliente MCP salvar o arquivo.

O limite é de 15 MiB de dados brutos por chamada (20 MiB em Base64, mais
metadados). O tamanho informado pelo Jira permite rejeição antecipada, mas o
limite também é aplicado ao ler a resposta. Acima disso, abra a issue no Jira
para baixar o arquivo.

No Cloud, o download usa a API autenticada do site (inclusive via gateway
OAuth), sem redirecionamento para o CDN. No Data Center, usa o endereço de
conteúdo retornado pelo Jira e, para a nova ferramenta, só aceita a origem
configurada; redirecionamentos não atravessam essa origem.
