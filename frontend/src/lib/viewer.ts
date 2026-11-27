import { createContext, useContext } from "react";

export type ViewerAccess = {
  ready: boolean;
  signedIn: boolean;
  accountId?: string;
  broadcastingStatus?: "not_applied" | "pending" | "approved" | "suspended";
  staff: boolean;
  canBroadcast: boolean;
};

export const ViewerContext = createContext<ViewerAccess>({
  ready: false,
  signedIn: false,
  staff: false,
  canBroadcast: false,
});

export function useViewer() {
  return useContext(ViewerContext);
}
