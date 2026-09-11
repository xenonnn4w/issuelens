# Console

`src/app.ts` is the strict TypeScript source for the console. `npm run build` bundles it with the Lucide icons used by the UI into the ignored `out/static/app.js`, which the Go backend serves locally and through Caddy in deployment. Neither compiled JavaScript nor vendored JavaScript is committed. Node is needed at build time, not at runtime.

```bash
npm ci
npm run typecheck
npm run build
```

The layout follows the TypeSafe console reference: light fixed sidebar, monochrome action buttons, blue links, and a two-column Home workspace. All navigation points to implemented Issue Lens views.

Home uses a compact header without an illustration. New investigations open their own `#new` page instead of using a form on Home.

Vendor assets are served locally: Lucide icons (npm `lucide`) and Inter fonts (npm `@fontsource/inter`). Their licenses are in `licenses/`. Personal connection keys are kept in tab-scoped session storage and sent only with investigation requests; they are not saved in shared reports. API calls are same-origin.

The root `make check` typechecks and builds the console. Backend/API tests live in `../backend/`.

Browser QA verified the desktop Home, Connections form, saved reports, approval controls, and Usage view, plus the Home and Connections layouts at a 390px mobile viewport. A live GitHub retrieval run was completed earlier.
