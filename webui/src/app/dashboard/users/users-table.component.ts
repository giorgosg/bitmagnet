import { Component } from "@angular/core";
import { UsersDatasource } from "../../auth/users.datasource";
import { AppModule } from "../../app.module";
import * as generated from "../../graphql/generated";
import { TimeAgoPipe } from "../../pipes/time-ago.pipe";
import { PaginatorComponent } from "../../paginator/paginator.component";

@Component({
  selector: "app-users-table",
  template: `
    <ng-container *transloco="let t">
      <table
        mat-table
        [dataSource]="dataSource"
        [multiTemplateDataRows]="true"
        class="table-results"
      >
        <ng-container matColumnDef="id">
          <th mat-header-cell *matHeaderCellDef>ID</th>
          <td mat-cell *matCellDef="let i">
            {{ item(i).id }}
          </td>
        </ng-container>

        <ng-container matColumnDef="username">
          <th mat-header-cell *matHeaderCellDef>
            {{ t("auth.username") }}
          </th>
          <td mat-cell *matCellDef="let i">
            {{ item(i).username }}
          </td>
        </ng-container>

        <ng-container matColumnDef="role">
          <th mat-header-cell *matHeaderCellDef>
            {{ t("auth.role") }}
          </th>
          <td mat-cell *matCellDef="let i">
            {{ item(i).role }}
          </td>
        </ng-container>

        <ng-container matColumnDef="email">
          <th mat-header-cell *matHeaderCellDef>
            {{ t("general.email") }}
          </th>
          <td mat-cell *matCellDef="let i">
            {{ item(i).email }}
          </td>
        </ng-container>

        <ng-container matColumnDef="createdAt">
          <th mat-header-cell *matHeaderCellDef>
            {{ t("general.created_at") }}
          </th>
          <td mat-cell *matCellDef="let i" [matTooltip]="item(i).createdAt">
            {{ item(i).createdAt | timeAgo }}
          </td>
        </ng-container>

        <ng-container matColumnDef="lastLoginAt">
          <th mat-header-cell *matHeaderCellDef>
            {{ t("auth.last_login_at") }}
          </th>
          <td
            mat-cell
            *matCellDef="let i"
            [matTooltip]="i.lastLoginAt ?? t('general.never')"
          >
            @if (i.lastLoginAt; as lastLoginAt) {
              {{ lastLoginAt | timeAgo }}
            } @else {
              {{ t("general.never") }}
            }
          </td>
        </ng-container>

        <tr mat-header-row *matHeaderRowDef="displayedColumns"></tr>
        <tr mat-row *matRowDef="let i; columns: displayedColumns"></tr>
      </table>
      <app-paginator
        (paging)="dataSource.handlePagination($event)"
        [page]="dataSource.page"
        [pageSize]="dataSource.limit"
        [pageLength]="(dataSource.users$ | async)?.length ?? 0"
        [totalLength]="(dataSource.result$ | async)?.totalCount ?? 0"
        [totalIsEstimate]="false"
        [showLastPage]="true"
      />
    </ng-container>
  `,
  styles: [
    `
      tr:not(.expanded-detail-row) td {
        cursor: pointer;
      }

      app-paginator {
        margin-top: 10px;
        float: right;
      }
    `,
  ],
  standalone: true,
  imports: [AppModule, PaginatorComponent, TimeAgoPipe],
})
export class UsersTableComponent {
  dataSource = new UsersDatasource();

  displayedColumns = [
    "id",
    "username",
    "role",
    "email",
    "createdAt",
    "lastLoginAt",
  ];

  item(item: unknown): generated.User {
    return item as generated.User;
  }
}
