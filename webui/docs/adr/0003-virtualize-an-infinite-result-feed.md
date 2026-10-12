# Virtualize an infinite result feed with deterministic row heights

Search results append as the user scrolls and only visible rows are rendered. Expansion stays inline, every display state has a deterministic height, and the result list is the page's sole scrolling element so one scroll handler can drive both virtualization and fetching. This fits scanning a continuously changing DHT index better than numbered pages and keeps long sessions responsive, while accepting that page numbers and scroll depth are not addressable and that Elm height calculations and CSS must remain synchronized.
