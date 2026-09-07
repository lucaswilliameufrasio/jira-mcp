# Jira MCP Documentation Site

Static Astro site for the public Jira MCP documentation and landing page.

## Development

```bash
pnpm install --frozen-lockfile
pnpm dev
```

## Production Build

```bash
pnpm build
pnpm preview
```

The generated site is written to `dist/`. This project is intentionally static:
there is no SSR adapter, server runtime, or secret required by the site.

## Deployment

See [`../docs/deployment/cloudflare-pages.md`](../docs/deployment/cloudflare-pages.md)
for the Cloudflare Pages configuration, production branch, build output, and
build watch path.
