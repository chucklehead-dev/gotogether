// Types of gotogether-requests.cjs: a Playwright page's requests as a
// gotogether request log (gotogether.requests/1).
import type { Page } from 'playwright';

/** A request as classify sees it. */
export interface Req {
  url: string;
  method: string;
  headers: Record<string, string>;
  /** the request's body: parsed JSON, or its text, or null */
  body: unknown;
}

/** An answer as answer sees it (status 0, body null and error: none came). */
export interface Res {
  status: number;
  headers: Record<string, string>;
  body: unknown;
  error?: string;
}

/** A model call: the action a request invokes, and what it sent. */
export interface Call {
  action: string;
  args?: Record<string, unknown>;
  /** another tab's program order */
  thread?: string;
}

/** A step the page shows, answering no request. */
export interface Step {
  action: string;
  args?: Record<string, unknown>;
  obs?: Record<string, unknown>;
  ref?: string;
  thread?: string;
}

export interface Options {
  /** the log's file, written line by line */
  path: string;
  /** the log's header: recorder required */
  header: { recorder: string; actor?: string; process?: string; thread?: string; driver?: string };
  /** which requests are model calls, and what each sent (null: not one) */
  classify: (req: Req) => Call | null | undefined;
  /** the answer's fields the model reads (default: the JSON body's scalar fields) */
  answer?: (res: Res, call: Call) => Record<string, unknown>;
  /** the header a call's correlation id goes in (default x-request-id), and its prefix (default "run") */
  correlation?: { header?: string; prefix?: string };
  /** a request's ref (default "METHOD /path") */
  ref?: (req: Req, call: Call) => string;
  /** the route pattern of the requests looked at (default every one) */
  match?: string;
  /** how long an answer's body is waited for, in ms (default 10000) */
  bodyTimeout?: number;
  /** a file for the events too, for replay without a browser */
  events?: string;
}

export interface RequestLogHandle {
  /** writes a step now */
  step(action: string, args?: Record<string, unknown>, extra?: Omit<Step, 'action' | 'args'>): Promise<void>;
  /** writes map(text)'s step each time the selector's text changes */
  watch(selector: string, map: (text: string | null) => Step | null | undefined): Promise<void>;
  /** stops routing, waits for the answers being read, closes the files */
  close(): Promise<void>;
}

export const PROTOCOL: 'gotogether.requests/1';
export function requestLog(page: Page, opts: Options): Promise<RequestLogHandle>;
export function replay(events: string, opts: Pick<Options, 'classify' | 'answer' | 'ref'>, write: (s: string) => void): void;
export class RequestLog {
  constructor(opts: Omit<Options, 'path' | 'match' | 'events' | 'bodyTimeout'>, write: (s: string) => void);
}
