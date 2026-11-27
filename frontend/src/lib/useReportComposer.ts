import { createContext, useContext } from "react";

import type { LiveComment } from "./comments";

export type ReportTarget = { streamId: string; comment: LiveComment };
export const ReportContext = createContext<(target: ReportTarget) => void>(() => {});
export function useReportComposer() { return useContext(ReportContext); }
