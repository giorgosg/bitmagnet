# Put shareable search state in real URLs

Search terms, sorting, and facet filters live in query parameters on ordinary paths, with `Route.toHref` kept as the inverse of the parser. Ephemeral row expansion and scroll depth remain local state; rapid query edits use history replacement while committed searches and sort changes add history entries. This makes searches bookmarkable and browser history meaningful at the cost of requiring an `index.html` fallback for deep links and deliberately not restoring a long feed's scroll position.
