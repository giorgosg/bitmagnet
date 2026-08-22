import { inject, Injectable } from "@angular/core";
import { Apollo } from "apollo-angular";
import {
  catchError,
  from,
  map,
  BehaviorSubject,
  Observable,
  filter,
  take,
  retry,
  EMPTY,
  distinctUntilChanged,
} from "rxjs";
import * as generated from "../graphql/generated";
import { filterComplete } from "../graphql/util/filter-complete";
import { AuthTokenService } from "./auth-token.service";
import { newEnforcer, ObjectAction as _objectAction } from "./enforcer";

/**
 * Adapted from upstream/next's auth service. Two differences of substance:
 *
 * - next detects an expired session from a GraphQL error extension emitted by
 *   its plugin error registry, using Apollo Client v4's CombinedGraphQLErrors.
 *   This lineage is on v3 and has no such registry. It also always resolves an
 *   identity, falling back to the anonymous one, so an expired token produces a
 *   successful response with a null user rather than an error. Holding a token
 *   while the server reports no user is therefore the signal that it is stale.
 * - errors surface as plain Error, matching how the rest of this UI handles them.
 */
const pollInterval = 10000;

type LoginResult = generated.LoginMutation["self"]["login"];
type RegisterResult = generated.RegisterMutation["self"]["register"];

export type ObjectAction = _objectAction;

@Injectable({ providedIn: "root" })
export class AuthService {
  private apollo = inject(Apollo);
  private tokenService = inject(AuthTokenService);
  private watchQuery = this.apollo.watchQuery<
    generated.IdentityQuery,
    generated.IdentityQueryVariables
  >({
    query: generated.IdentityDocument,
    fetchPolicy: "no-cache",
    pollInterval,
  });
  private selfSubject = new BehaviorSubject<generated.SelfFragment | null>(
    null,
  );
  private loginErrorSubject = new BehaviorSubject<string | null>(null);
  private registerErrorSubject = new BehaviorSubject<string | null>(null);

  self$ = this.selfSubject
    .asObservable()
    .pipe(filter((self): self is generated.SelfFragment => !!self));

  enforcer$ = this.self$.pipe(
    map(({ permissions }) =>
      newEnforcer(permissions.map((objectAction) => ({ objectAction }))),
    ),
  );

  loginError$ = this.loginErrorSubject
    .asObservable()
    .pipe(distinctUntilChanged());

  registerError$ = this.registerErrorSubject
    .asObservable()
    .pipe(distinctUntilChanged());

  token$ = this.tokenService.token$;

  constructor() {
    this.watchQuery.valueChanges
      .pipe(
        filterComplete(),
        map((result) => {
          const self = result.data.self.identity;

          // A token that no longer resolves to a user is expired or revoked.
          if (!self.user && this.tokenService.getToken()) {
            this.tokenService.clearToken();
          }

          this.selfSubject.next(self);

          return result;
        }),
        retry({
          delay: 5000,
        }),
      )
      .subscribe();
  }

  public login(username: string, password: string): Observable<LoginResult> {
    this.resetLoginError();

    return this.apollo
      .mutate<generated.LoginMutation, generated.LoginMutationVariables>({
        mutation: generated.LoginDocument,
        variables: {
          username,
          password,
        },
      })
      .pipe(
        map((result) => {
          const login = result.data!.self.login;
          this.resetLoginError();
          this.tokenService.setToken(login.token);
          this.selfSubject.next({
            user: login.user,
            apiKey: null,
            permissions: login.permissions.map((p) => p.objectAction),
          });

          return login;
        }),
        catchError((error: Error) => {
          this.loginErrorSubject.next(error.message);

          return EMPTY;
        }),
        take(1),
      );
  }

  public register(input: generated.RegisterInput): Observable<RegisterResult> {
    this.resetRegisterError();

    return this.apollo
      .mutate<generated.RegisterMutation, generated.RegisterMutationVariables>({
        mutation: generated.RegisterDocument,
        variables: {
          input,
        },
      })
      .pipe(
        map((result) => result.data!.self.register),
        catchError((error: Error) => {
          this.registerErrorSubject.next(error.message);

          return EMPTY;
        }),
        take(1),
      );
  }

  public resetLoginError() {
    this.loginErrorSubject.next(null);
  }

  public resetRegisterError() {
    this.registerErrorSubject.next(null);
  }

  public logout(): Observable<generated.SelfFragment> {
    this.tokenService.clearToken();

    return from(this.watchQuery.refetch()).pipe(
      take(1),
      map((result) => {
        const self = result.data.self.identity;
        this.selfSubject.next(self);

        return self;
      }),
    );
  }

  public enforce(...objectActions: ObjectAction[]): Observable<boolean> {
    return this.enforcer$.pipe(
      map((enforcer) => objectActions.every((objAct) => !!enforcer(objAct))),
    );
  }

  public hasRole(role: string): Observable<boolean> {
    return this.self$.pipe(map((self) => self.user?.role === role));
  }

  public passwordEntropy(
    password: string,
  ): Observable<generated.PasswordEntropyResultFragment> {
    return this.apollo
      .query<
        generated.PasswordEntropyQuery,
        generated.PasswordEntropyQueryVariables
      >({
        query: generated.PasswordEntropyDocument,
        variables: {
          password,
        },
        fetchPolicy: "no-cache",
      })
      .pipe(map((result) => result.data.self.passwordEntropy));
  }
}
