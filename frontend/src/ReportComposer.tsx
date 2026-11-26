import { useState } from "react";
import type { ReactNode } from "react";
import ReportUser from "./ReportUser";

import { ReportContext } from "./lib/useReportComposer";
import type { ReportTarget } from "./lib/useReportComposer";

// Owned by the route, so playback and comment-list removal cannot erase a draft.
export default function ReportComposer({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<ReportTarget>();
  return <ReportContext.Provider value={setTarget}>
    {children}
    {target && <ReportUser key={`${target.streamId}/${target.commentId}`} targetId={target.accountId} streamId={target.streamId} relatedCommentId={target.commentId} initiallyOpen />}
  </ReportContext.Provider>;
}
