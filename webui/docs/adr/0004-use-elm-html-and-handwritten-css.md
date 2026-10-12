# Use elm/html and handwritten CSS for precise layout

Magnes uses `elm/html` with a handwritten stylesheet rather than a higher-level layout system such as `elm-ui`. Native CSS provides direct text truncation inside shrinking flex rows and exact pixel heights for the virtualizer, both of which are central to the result feed. The trade-off is a manual contract between Elm's height calculations and CSS, but moving that layout into another algebra later would be more complex and obscure the measurements the feed depends on.
