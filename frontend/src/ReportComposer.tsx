import { useState } from "react";
import type { ReactNode } from "react";
import ReportUser from "./ReportUser";

import { ReportContext } from "./lib/useReportComposer";
import type { ReportTarget } from "./lib/useReportComposer";

// Owned by the route, so playback and comment-list removal cannot erase a draft.
export default function ReportComposer({ children }: { children: ReactNode }) {
  const [report, setReport] = useState<{ target: ReportTarget; openRequest: number }>();
  function openReport(target: ReportTarget) {
    setReport((current) => ({ target, openRequest: (current?.openRequest ?? 0) + 1 }));
  }
  const target = report?.target;
  return <ReportContext.Provider value={openReport}>
    {children}
    {report && target && <ReportUser key={`${target.streamId}/${target.comment.id}`}
      targetId={target.comment.accountId} streamId={target.streamId} relatedComment={target.comment}
      openRequest={report.openRequest} />}
  </ReportContext.Provider>;
}
