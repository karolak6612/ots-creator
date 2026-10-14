// Tibia-style replacement for window.confirm. ConfirmDialog (mounted in
// App) renders the pending question; ask() resolves with the answer.

export interface Question {
  title: string;
  message: string;
  /** Label of the confirming button. */
  ok: string;
  /** Red confirming button; on by default for removing words (Remove, Delete…). */
  danger?: boolean;
}

/** Labels of buttons that destroy something: shown red. */
export const DESTRUCTIVE = /^(remove|delete|discard|clear|erase|reset)\b/i;

export const confirmState = $state<{ question: (Question & { resolve: (yes: boolean) => void }) | null }>({ question: null });

/** Shows a confirmation dialog. A second question cancels the first. */
export function ask(q: Question): Promise<boolean> {
  confirmState.question?.resolve(false);
  return new Promise((resolve) => {
    confirmState.question = { ...q, resolve };
  });
}

/** Answers the pending question. */
export function answer(yes: boolean): void {
  const q = confirmState.question;
  confirmState.question = null;
  q?.resolve(yes);
}

export interface TextQuestion {
  title: string;
  label: string;
  value?: string;
  ok?: string;
}

export const promptState = $state<{ question: (TextQuestion & { resolve: (v: string | null) => void }) | null }>({ question: null });

/** Asks for a line of text (Tibia-style window.prompt); null when cancelled. */
export function askText(q: TextQuestion): Promise<string | null> {
  promptState.question?.resolve(null);
  return new Promise((resolve) => {
    promptState.question = { ...q, resolve };
  });
}

/** Answers the pending text question. */
export function answerText(v: string | null): void {
  const q = promptState.question;
  promptState.question = null;
  q?.resolve(v);
}
