import {
  Router,
  Routes,
  CanActivateFn,
  RouterStateSnapshot,
} from "@angular/router";
import { inject } from "@angular/core";
import { map } from "rxjs";
import { AuthService } from "./auth/auth.service";

const navigateToLogin = (router: Router, state: RouterStateSnapshot) => {
  void router.navigate(["/login"], {
    queryParams: { returnUrl: state.url },
  });
};

const requireUserGuard =
  (
    require: boolean,
    onFailed: (router: Router, state: RouterStateSnapshot) => void,
  ): CanActivateFn =>
  ({}, state) => {
    const authService = inject(AuthService);
    const router = inject(Router);
    return authService.self$.pipe(
      map(({ user }) => {
        if (require !== !user) {
          return true;
        }

        onFailed(router, state);

        return false;
      }),
    );
  };

export const routes: Routes = [
  {
    path: "login",
    canActivate: [
      requireUserGuard(false, (router) => void router.navigate(["/account"])),
    ],
    loadComponent: () =>
      import("./auth/login.component").then((c) => c.LoginComponent),
  },
  {
    path: "register",
    canActivate: [
      requireUserGuard(false, (router) => void router.navigate(["/account"])),
    ],
    loadComponent: () =>
      import("./auth/register.component").then((c) => c.RegisterComponent),
  },
  {
    path: "account",
    canActivate: [requireUserGuard(true, navigateToLogin)],
    loadComponent: () =>
      import("./account/account.component").then((c) => c.AccountComponent),
    children: [
      {
        path: "",
        loadComponent: () =>
          import("./account/account-home.component").then(
            (c) => c.AccountHomeComponent,
          ),
      },
      {
        path: "api-keys",
        loadComponent: () =>
          import("./account/api-keys-panel.component").then(
            (c) => c.APIKeysPanelComponent,
          ),
      },
    ],
  },
  {
    path: "",
    pathMatch: "full",
    redirectTo: "torrents",
  },
  {
    path: "torrents",
    loadComponent: () =>
      import("./torrents/torrents.component").then((c) => c.TorrentsComponent),
    children: [
      {
        path: "",
        loadComponent: () =>
          import("./torrents/torrents-search.component").then(
            (c) => c.TorrentsSearchComponent,
          ),
      },
      {
        path: "permalink/:infoHash",
        loadComponent: () =>
          import("./torrents/torrent-permalink.component").then(
            (c) => c.TorrentPermalinkComponent,
          ),
      },
    ],
  },
  {
    path: "dashboard",
    loadComponent: () =>
      import("./dashboard/dashboard.component").then(
        (c) => c.DashboardComponent,
      ),
    children: [
      {
        path: "",
        loadComponent: () =>
          import("./dashboard/dashboard-home.component").then(
            (c) => c.DashboardHomeComponent,
          ),
      },
      {
        path: "queues",
        pathMatch: "full",
        redirectTo: "queues/visualize",
      },
      {
        path: "queues",
        loadComponent: () =>
          import("./dashboard/queue/queue-dashboard.component").then(
            (c) => c.QueueDashboardComponent,
          ),
        children: [
          {
            path: "visualize",
            loadComponent: () =>
              import("./dashboard/queue/queue-visualize.component").then(
                (c) => c.QueueVisualizeComponent,
              ),
          },
          {
            path: "jobs",
            loadComponent: () =>
              import("./dashboard/queue/queue-jobs.component").then(
                (c) => c.QueueJobsComponent,
              ),
          },
          {
            path: "admin",
            loadComponent: () =>
              import("./dashboard/queue/queue-admin.component").then(
                (c) => c.QueueAdminComponent,
              ),
          },
        ],
      },
      {
        path: "users",
        loadComponent: () =>
          import("./dashboard/users/users-admin.component").then(
            (c) => c.UsersAdminComponent,
          ),
      },
      {
        path: "roles",
        loadComponent: () =>
          import("./dashboard/roles/roles-admin.component").then(
            (c) => c.RolesAdminComponent,
          ),
      },
      {
        path: "invitations",
        loadComponent: () =>
          import("./dashboard/invitations/invitations-admin.component").then(
            (c) => c.InvitationsAdminComponent,
          ),
      },
      {
        path: "torrents",
        loadComponent: () =>
          import("./dashboard/torrents/torrents-dashboard.component").then(
            (c) => c.TorrentsDashboardComponent,
          ),
      },
    ],
  },
  {
    path: "**",
    loadComponent: () =>
      import("./not-found/not-found.component").then(
        (c) => c.NotFoundComponent,
      ),
  },
];
