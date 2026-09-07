# Deploying the Documentation Site to Cloudflare Pages

The documentation site is a static Astro project in `docs-site/`. It does not
need an SSR adapter, Workers runtime, server functions, or Jira credentials.

## Cloudflare Pages Settings

Create a Pages project connected to the GitHub repository with these settings:

| Setting | Value |
|---|---|
| Production branch | `main` |
| Root directory | `docs-site` |
| Framework preset | Astro, or custom |
| Build command | `pnpm build` |
| Build output directory | `dist` |
| Node.js version | `22.13.0` or newer (pnpm 11 requires it) |
| Package manager | pnpm |

Because the root directory is `docs-site`, the output directory is relative to
that directory. Do not configure `docs-site/dist` as the output path when the
root directory is already set.

## Build Watch Paths

Configure Cloudflare Pages Git integration to watch only:

```text
docs-site/**
```

If the Pages dashboard presents watch paths as repository-relative patterns,
use the exact pattern above. Verify the setting with a non-site commit: changes
only under Go source, tests, `.agents/`, or `docs/` must not create a Pages
deployment. A change under `docs-site/` must create one.

Keep the watch-path rule in the Pages project settings rather than adding a
runtime workaround to Astro. The site should remain a normal static build.

## Local Verification

```bash
cd docs-site
pnpm install --frozen-lockfile
pnpm build
pnpm preview
```

The production output is `docs-site/dist/` and must not be committed.

## Pull Requests and Production

- Pull Requests should use Pages preview deployments.
- `main` is the production branch.
- Keep production secrets out of Pages environment variables; this site is
  static and should not need secrets.
- Use the Pages deployment history to roll back to a previous successful build.
- Configure the custom domain and HTTPS in Cloudflare Pages after the first
  successful production deployment.

## Troubleshooting

- If the build cannot find `pnpm`, configure the Pages Node/pnpm versions or
  use the repository's supported package-manager setup.
- If Pages cannot find the output, check that the root is `docs-site` and the
  output is `dist`, not `docs-site/dist`.
- If unrelated commits trigger builds, re-check the repository-relative watch
  path and the Pages root-directory setting.
- If the site expects a server runtime, remove that expectation; this project is
  intentionally static.
