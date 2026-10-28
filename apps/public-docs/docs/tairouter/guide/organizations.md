# Organizations and invitations

The console provides organization selection, creation, invitation acceptance, member administration, ownership transfer, and account deletion. The procedures below describe these workflows; their availability in code and mocked UI tests does not establish live OIDC or SMTP acceptance.

## Choose an organization

After OIDC login, create an organization or accept an invitation. Select the intended organization before adding providers, routes, or keys. Your credentials, pools, routing, and temporary conversations in one organization cannot be used in another. Organizations do not share provider credential pools.

| Role              | Authority                                                                                                 |
| ----------------- | --------------------------------------------------------------------------------------------------------- |
| Member            | Manage and use permitted personal resources                                                               |
| Administrator     | Invite/remove ordinary members and manage organization settings                                           |
| Owner             | Administrator functions, organization deletion, ownership transfer, and administrator appointment/removal |
| Instance operator | Separate instance configuration and whole-instance health/aggregates                                      |

Administrators cannot change another administrator's or the owner's role/membership. Owners and administrators can inspect member usage, estimated cost, model, and error metadata, but cannot read another member's provider secrets, prompts, or responses. Permission changes apply to subsequent requests even if a page was already open.

## Open and share a console page

Console pages include the organization in the path, for example `/organizations/<organization-id>/keys`, `/organizations/<organization-id>/providers`, `/organizations/<organization-id>/routing`, and `/organizations/<organization-id>/organization-usage`. Opening or refreshing the address keeps that page and workspace; current membership and permissions still apply. Internal links open without reloading the page, and browser Back and Forward restore the page and organization. Existing bookmarks such as `/keys?organization=<organization-id>` open at the corresponding path address.

Open `/overview` to choose an organization and `/invitation` to accept an invitation. Legacy `?page=` addresses, path aliases, and `org` or `organization_id` workspace selectors are no longer supported. Use the organization path address. Instance operators can also open individual sections, such as `/instance/settings` for request limits and `/instance/health` for health and usage aggregates.

## Invite and accept

1. An authorized owner or administrator chooses Invite member, enters the intended email address and selects Send invitation. Invitees join as members, and promotion to administrator is managed separately by an owner. Creating an invitation starts delivery; it does not confirm that an email was sent.
2. The recipient opens the invitation and signs in through OIDC.
3. The account's verified email must match the invitation email.
4. The recipient accepts within seven days. Each link can be used once.

Cancel an invitation that is no longer needed: choose Cancel invitation, type the invited email address exactly as shown, and select Confirm. The invitation link stops working. Wait at least one minute between resends to the same email. A manual resend invalidates the old link and pending delivery jobs, then starts a new seven-day lifetime. Use the newest email.

SMTP delivery allows at most three attempts: the initial send, a retry after one minute, then a retry five minutes after the next failure. Only transient errors retry. Permanent errors fail immediately. Each attempt counts toward 100 attempts per organization per hour. Limited attempts wait for capacity but never send after expiry. In the invitation list, Sent means the email was submitted to the SMTP server; it does not confirm recipient delivery or acceptance, and a stored attempt time does not confirm a scheduled delivery.

Automatic retries reuse the link; ambiguous SMTP completion can produce duplicate emails. Acceptance remains single-use. Final delivery failure invalidates the link. Recover by requesting a manual resend rather than reusing that link.

## Transfer ownership

The owner nominates an existing member. The member must accept within seven days; the previous owner then becomes an administrator. Only one transfer can be pending. A replacement invalidates the earlier request, and the owner can cancel before acceptance. Both parties must still have the required membership and authority at acceptance.

The last owner must transfer ownership or delete the organization before leaving.

## Leave, remove, or delete

Leaving or removal immediately revokes that user's organization keys and deletes personal provider credentials, configuration, and temporary state in that organization. Rejoining requires reconfiguration. Usage metadata remains for its retention period. An upstream response already dispatched can finish, but further retries, account switches, and token refresh must stop.

Only the owner can delete an organization. Deletion removes its credentials, settings, temporary state, usage, and organization audits. To delete your user account, first transfer or delete organizations you own. Account deletion removes memberships and personal identifying information and anonymizes retained usage and surviving audit actors. Existing backups remain subject to the operator's retention policy.
