/**
 * What an island renders when `/api/admin/me` answered 401.
 *
 * Shared by the job list and the job form so the two cannot drift into telling
 * an admin two different stories about the same condition.
 *
 * The action is a reload, not a link to a sign-in page — there is no sign-in
 * page, and redirecting to one would loop. `adminIdentity.ts` has the full
 * reasoning; the short version is that Cloudflare Access authenticates the
 * *page request* at the edge, so re-requesting the page is the only thing that
 * can change the outcome.
 *
 * It is a `<button>` rather than an `<a href>` because it performs an action on
 * the current page rather than navigating to a different resource, and a link
 * whose target is "wherever you already are" is a lie to a screen reader.
 */
import {
  ACCESS_NOTICE_ACTION,
  ACCESS_NOTICE_BODY,
  ACCESS_NOTICE_TITLE,
  reloadPage,
} from '@lib/adminIdentity';

export default function AccessNotice() {
  return (
    // `role="status"` + polite: the notice is what the region contains on first
    // paint, so there is nothing for an assertive live region to interrupt.
    <div className="jpa-state" role="status" aria-live="polite">
      <strong>{ACCESS_NOTICE_TITLE}</strong>
      <span>{ACCESS_NOTICE_BODY}</span>
      <button type="button" className="jpa-btn jpa-btn--primary" onClick={reloadPage}>
        {ACCESS_NOTICE_ACTION}
      </button>
    </div>
  );
}
