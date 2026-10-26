import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { markdown } from "@codemirror/lang-markdown";
import { EditorState } from "@codemirror/state";
import { EditorView, keymap, placeholder } from "@codemirror/view";
import { useEffect, useRef } from "react";

export interface MarkdownEditorProps {
  value: string;
  /** The user may change the text; otherwise it only reads. */
  editable: boolean;
  onChange?: (text: string) => void;
  label: string;
}

/**
 * The Markdown editor of a scroll (CodeMirror 6). It is uncontrolled while the person
 * types and takes a new `value` in when it differs from the text it holds (what an
 * agent wrote), which the caller only passes while there is no unsent change of the person.
 */
export function MarkdownEditor({ value, editable, onChange, label }: MarkdownEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const initial = useRef(value);
  const change = useRef(onChange);
  change.current = onChange;

  useEffect(() => {
    if (!host.current) return;
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: initial.current,
        extensions: [
          history(),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          markdown(),
          EditorView.lineWrapping,
          EditorState.readOnly.of(!editable),
          EditorView.editable.of(editable),
          EditorView.contentAttributes.of({ "aria-label": label }),
          placeholder(""),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) change.current?.(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    return () => {
      v.destroy();
      view.current = null;
    };
  }, [editable, label]);

  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
    }
  }, [value]);

  return <div ref={host} className="md-editor nodrag nowheel nopan h-full min-h-0 overflow-auto" />;
}
