// Bombadil's specification of the webjobs page: what it may do (submit a
// report, with a double click as people do; cancel the job shown; wait)
// and what it checks of the page by itself. The model checks the rest,
// from this run's trace.jsonl (package bombadiltrace) and the server's
// steps. The adapter reads the custom actions by name (ui.submit,
// ui.cancel) and the `result` snapshot: keep the three as they are.
import { always, now, eventually } from "@antithesishq/bombadil";
import { extract, actions, registerCustomAction } from "@antithesishq/bombadil/browser";

// What the page shows: the job of the last answer, as JSON, and the key
// of the submission the form would send.
const result = extract(({ document }) => {
  const text = document.querySelector("#result")?.textContent?.trim() ?? "";
  let job: { id?: string; status?: string } = {};
  try {
    job = text ? JSON.parse(text) : {};
  } catch {
    // shown as it is: the adapter refuses a result it cannot read
  }
  const form = document.querySelector<HTMLFormElement>("#form");
  return {
    text,
    id: job.id ?? "",
    status: job.status ?? "",
    key: form?.dataset.key ?? "",
    canSubmit: !(document.querySelector<HTMLButtonElement>("#submit")?.disabled ?? true),
    canCancel: !(document.querySelector<HTMLButtonElement>("#cancel")?.disabled ?? true),
  };
});

// A double click: the second submission of one key must ask for nothing
// more (the server's created steps say whether it did).
const submit = registerCustomAction("ui.submit", async (document) => {
  const button = document.querySelector<HTMLButtonElement>("#submit");
  if (!button || button.disabled) throw new Error("submit is not enabled");
  button.click();
  button.click();
});

const cancel = registerCustomAction("ui.cancel", async (document) => {
  const button = document.querySelector<HTMLButtonElement>("#cancel");
  if (!button || button.disabled) throw new Error("cancel is not enabled");
  button.click();
});

// Mostly waiting, so that most jobs run to ready and some are cancelled
// (a job runs for 200 to 600ms, and Bombadil acts every 50ms or so).
export const journey = actions(() => {
  const branches: [number, { value: any }][] = [[10, { value: "Wait" }]];
  if (result.current.canSubmit) branches.push([4, { value: submit() }]);
  if (result.current.canCancel) branches.push([1, { value: cancel() }]);
  return { branches };
});

// The page's own checks: a result is a job, and a job accepted ends
// (ready or cancelled) within five seconds.
export const resultIsAJob = always(
  () => result.current.text === "" || (/^j[0-9]+$/.test(result.current.id) && ["accepted", "ready", "cancelled"].includes(result.current.status)),
);

export const acceptedEnds = always(
  now(() => result.current.status === "accepted").implies(eventually(() => result.current.status !== "accepted").within(5, "seconds")),
);
