import { Plus, Trash2 } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { ago, plural } from "../lib";
import type { Item } from "../types";
import { Dialog, Dot, Empty, ErrorNote, Field, Figure, SectionHead, useAction } from "./ui";

/** The template's example page: figures, a list, and an add/edit dialog. */
export function ItemsPage() {
  const { data: items, error, reload } = useData(api.items, 30_000);
  const [editing, setEditing] = useState<Partial<Item> | null>(null);
  const toggle = useAction();

  const open = items?.filter((it) => !it.done) ?? [];
  const done = items?.filter((it) => it.done) ?? [];

  const setDone = (it: Item, value: boolean) =>
    toggle.run(async () => {
      await api.saveItem({ ...it, done: value });
      await reload();
    });

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Skeleton</div>
        <h1 class="page-title">Items</h1>
        <div class="figures stagger">
          <Figure value={items ? open.length : "—"} label="Open" tone="accent" />
          <Figure
            value={items ? done.length : "—"}
            unit={items ? `/${items.length}` : ""}
            label="Done"
          />
        </div>
      </header>

      {error && <ErrorNote>{error}</ErrorNote>}
      {toggle.error && <ErrorNote>{toggle.error}</ErrorNote>}

      <section class="section">
        <SectionHead index={1} title="Open">
          <button
            class="btn btn-primary btn-small"
            onClick={() => setEditing({ title: "", note: "" })}
          >
            <Plus size={14} /> Add
          </button>
        </SectionHead>
        {!items && <div class="loading loading-list" />}
        {items && open.length === 0 && <Empty>Nothing open. Add an item to get started.</Empty>}
        {open.length > 0 && (
          <ItemList items={open} onEdit={setEditing} onToggle={setDone} busy={toggle.busy} />
        )}
      </section>

      {done.length > 0 && (
        <section class="section">
          <SectionHead index={2} title="Done">
            <span class="muted">{plural(done.length, "item")}</span>
          </SectionHead>
          <ItemList items={done} onEdit={setEditing} onToggle={setDone} busy={toggle.busy} />
        </section>
      )}

      {editing && (
        <ItemDialog
          item={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </div>
  );
}

function ItemList(props: {
  items: Item[];
  busy: boolean;
  onEdit: (it: Item) => void;
  onToggle: (it: Item, done: boolean) => void;
}) {
  return (
    <div class="list">
      {props.items.map((it) => (
        <div key={it.id} class="list-row">
          <label class="check">
            <input
              type="checkbox"
              checked={it.done}
              disabled={props.busy}
              aria-label={it.done ? "Mark open" : "Mark done"}
              onChange={(e) => props.onToggle(it, e.currentTarget.checked)}
            />
          </label>
          <Dot tone={it.done ? "" : "accent"} />
          <button class="list-main list-link" onClick={() => props.onEdit(it)}>
            <span class="list-title">{it.title}</span>
            <span class="list-sub">{[it.note, ago(it.created)].filter(Boolean).join(" · ")}</span>
          </button>
        </div>
      ))}
    </div>
  );
}

function ItemDialog(props: { item: Partial<Item>; onClose: () => void; onSaved: () => void }) {
  const [item, setItem] = useState(props.item);
  const { busy, error, run } = useAction();

  const save = (e: Event) => {
    e.preventDefault();
    run(async () => {
      await api.saveItem(item);
      props.onSaved();
    });
  };
  const remove = () =>
    run(async () => {
      if (!item.id || !confirm(`Delete “${item.title}”?`)) return;
      await api.deleteItem(item.id);
      props.onSaved();
    });

  return (
    <Dialog
      title={item.id ? "Edit item" : "New item"}
      onClose={props.onClose}
      footer={
        <>
          {item.id && (
            <button class="btn btn-danger" onClick={remove} disabled={busy}>
              <Trash2 size={14} /> Delete
            </button>
          )}
          <span class="spacer" />
          <button class="btn btn-ghost" onClick={props.onClose}>
            Cancel
          </button>
          <button class="btn btn-primary" form="item-form" disabled={busy || !item.title?.trim()}>
            Save
          </button>
        </>
      }
    >
      <form id="item-form" class="form" onSubmit={save}>
        <Field label="Title">
          <input
            class="input"
            value={item.title}
            autofocus
            onInput={(e) => setItem({ ...item, title: e.currentTarget.value })}
          />
        </Field>
        <Field label="Note" hint="Optional">
          <input
            class="input"
            value={item.note}
            onInput={(e) => setItem({ ...item, note: e.currentTarget.value })}
          />
        </Field>
        {error && <div class="form-error">{error}</div>}
      </form>
    </Dialog>
  );
}
