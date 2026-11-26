import CommentPermissionControl from "./CommentPermissionControl";
import { useEffect, useRef, useState } from "react";

type Report = { id: string; reporterId: string; targetId: string; reason: string; incidentAt: string; streamId: string | null; commentId: string | null; state: string; outcome: string | null; expiresAt: string };
export default function StaffReports() {
 const evidenceRequest=useRef(0); const [commentId,setCommentId]=useState(""); const [offset,setOffset]=useState(0); const [evidence,setEvidence]=useState<{kind:string;body:string | null;occurredAt:string}[]>([]); const [items,setItems]=useState<Report[]>([]); const [selected,setSelected]=useState<string | null>(null); const [outcome,setOutcome]=useState(""); const [status,setStatus]=useState(""); const [busy,setBusy]=useState(false);
 const listGeneration = useRef(0);
 const requestedPage = useRef(0);
 const committedPage = useRef(0);
 async function load(signal?: AbortSignal, page = requestedPage.current) {
   const generation = ++listGeneration.current;
   requestedPage.current = page;
   try {
     const response = await fetch(`/api/staff/reports?offset=${page}`, { signal });
     if (!response.ok) throw new Error("Could not load reports");
     const data = await response.json() as Report[];
     if (signal?.aborted || generation !== listGeneration.current) return false;
     setItems(data); setOffset(page); committedPage.current = page;
     return true;
   } catch (cause) {
     if (!signal?.aborted && generation === listGeneration.current) {
       requestedPage.current = committedPage.current;
       setStatus(String(cause));
     }
     return false;
   }
 }
 useEffect(() => {
   const controller = new AbortController();
   void Promise.resolve().then(() => { if (!controller.signal.aborted) return load(controller.signal, 0); });
   // These refs are request counters, not DOM nodes: invalidate the latest values.
   const invalidate = () => { listGeneration.current++; evidenceRequest.current++; };
   return () => { controller.abort(); invalidate(); };
 }, []);
 async function action(id:string,operation:string) {if(busy)return;setBusy(true);setStatus("");try{const r=await fetch(`/api/staff/reports/${id}/${operation}`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(operation === "delete-comment" ? {commentId: commentId.trim()} : {outcome})});if(!r.ok)throw new Error((await r.json()).error || "Action failed");if (await load()) setStatus(operation==="resolve"?"Report resolved":"Comment removed");}catch(e){setStatus(String(e))}finally{setBusy(false)}}
 const report=items.find(item=>item.id===selected);
 return <section aria-label="User reports"><h3>User reports</h3><button disabled={busy} onClick={()=>void load().catch(e=>setStatus(String(e)))}>Refresh reports</button><p>Page {Math.floor(offset / 100) + 1}</p><button disabled={busy || offset === 0} onClick={()=>{const page=Math.max(0,requestedPage.current-100);void load(undefined,page).catch(e=>setStatus(String(e)))}}>Previous</button><button disabled={busy || items.length < 100} onClick={()=>{const page=requestedPage.current+100;void load(undefined,page).catch(e=>setStatus(String(e)))}}>Next</button><ul>{items.map(item=><li key={item.id}><button onClick={()=>{setSelected(item.id);setOutcome("");setCommentId(item.commentId || "");setEvidence([]);const generation=++evidenceRequest.current;void fetch(`/api/staff/reports/${item.id}/evidence`,{signal:AbortSignal.timeout(10000)}).then(r=>{if(!r.ok)throw new Error("Could not load evidence");return r.json()}).then(data=>{if(generation===evidenceRequest.current)setEvidence(data)}).catch(e=>{if(generation===evidenceRequest.current)setStatus(String(e))})}}>{item.state} — {item.targetId.slice(0,12)} — {new Date(item.incidentAt).toLocaleString()}</button></li>)}</ul>
 {report && <article><h4>Report {report.id}</h4><p>Target: {report.targetId}</p><p>Reporter: {report.reporterId}</p><p>Incident: {new Date(report.incidentAt).toLocaleString()}</p><p style={{whiteSpace:"pre-wrap"}}>{report.reason}</p><p>Stream: {report.streamId || "none"}</p><p>Comment: {report.commentId || "none"}</p><p>Expires: {new Date(report.expiresAt).toLocaleString()}</p>{report.outcome && <p>Outcome: {report.outcome}</p>}
 <CommentPermissionControl key={report.id} accountId={report.targetId} reportId={report.id} />
 <h5>Retained evidence</h5>{evidence.map((entry,index)=><blockquote key={index}>{entry.kind} — {entry.body}</blockquote>)}
 <label>Comment ID to remove <input pattern="[a-fA-F0-9]{32}" value={commentId} onChange={e=>setCommentId(e.target.value)} /></label><button disabled={busy || !/^[a-fA-F0-9]{32}$/.test(commentId)} onClick={()=>void action(report.id,"delete-comment")}>Remove comment</button>
 {report.state==="open" && <form onSubmit={e=>{e.preventDefault();void action(report.id,"resolve")}}><label>Outcome <textarea required value={outcome} onChange={e=>setOutcome(e.target.value)} /></label><button disabled={busy}>Resolve report</button></form>}</article>}{status && <p role="status">{status}</p>}</section>;
}
