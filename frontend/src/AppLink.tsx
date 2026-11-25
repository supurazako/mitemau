import type { ReactNode } from "react";

export function AppLink({ href, children, className, "aria-current": current }: {
  href: string;
  children: ReactNode;
  className?: string;
  "aria-current"?: "page";
}) {
  return <a href={href} className={className} aria-current={current} onClick={(event) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    if (window.location.pathname === href) return;
    window.history.pushState(null, "", href);
    window.dispatchEvent(new PopStateEvent("popstate"));
    window.scrollTo(0, 0);
  }}>{children}</a>;
}
