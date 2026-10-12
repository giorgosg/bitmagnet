---
title: Contributing
description: Keep code and user documentation in step.
---

Start with [AGENTS.md](https://github.com/giorgosg/magnes/blob/trunk/AGENTS.md) for
repository rules and [the architecture map](https://github.com/giorgosg/magnes/blob/trunk/docs/architecture/README.md)
for subsystem orientation. Elm development notes live under `webui/docs/`.

The public site is in `website/`. Its pages are Markdown or MDX under
`website/src/content/docs/`. Run `npm ci` and `npm run dev` from that directory to
preview it. Before handing off a change, run `npm run build`; this checks types,
produces static pages and search, and checks local links for the GitHub Pages subpath.
Use the repository's pinned Prettier for supported source formats.

Update the relevant user guide when a default, workflow, or deployment changes.
Check examples against the implementation. Keep each explanation in one source;
link to detailed contributor notes rather than copying the whole architecture here.
