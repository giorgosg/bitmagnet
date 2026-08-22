import { ApolloQueryResult } from "@apollo/client/core";
import { filter, OperatorFunction } from "rxjs";

/**
 * Adapted from upstream/next, which expresses this against Apollo Client v4's
 * `dataState`. This lineage is on v3, where a watch query emits loading and
 * partial results with the same shape, so completeness is loading/error/data.
 */
export function filterComplete<T>(): OperatorFunction<
  ApolloQueryResult<T>,
  ApolloQueryResult<T>
> {
  return filter(
    (result: ApolloQueryResult<T>) =>
      !result.loading && !result.error && !!result.data,
  );
}
