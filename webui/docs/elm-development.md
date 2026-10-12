# Elm development

Repository-specific rules for planning and implementing Elm features.

## Search packages before designing a feature

Always search the Elm package registry before thinking through a new feature implementation, even when a custom implementation initially appears small or obvious. The search belongs at the start of design, not after custom code has already been planned.

1. Fetch the complete registry:

   ```bash
   curl -s --compressed https://package.elm-lang.org/search.json -o /tmp/elm-packages.json
   ```

2. Search both package names and summaries; Elm package names are often not the term used for the capability.
3. Check each candidate's `elm.json` and require Elm 0.19 compatibility.
4. Read the candidate's source, not only its README or generated documentation.
5. Prefer `elm/*` and `elm-community/*` packages when they cover the requirement.

Only design a custom implementation after this check. When the package search influences a tracked feature, record the credible candidates and why they were adopted or rejected so the investigation is not repeated.
