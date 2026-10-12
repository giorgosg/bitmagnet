# Replace the fork's Angular UI with Magnes

Magnes and the target bitmagnet fork evolve in parallel, and Magnes supports only that fork's current contract rather than carrying adapters for older schemas. Server behavior is first covered by automated tests and then exercised through Magnes as each workflow lands; once the replacement is complete enough, Magnes replaces the fork's Angular UI rather than extending the incomplete Angular port.
