import { createContext, useContext } from "react";

export type ReportTarget = { accountId: string; streamId: string; commentId: string };
export const ReportContext = createContext<(target: ReportTarget) => void>(() => {});
export function useReportComposer() { return useContext(ReportContext); }
