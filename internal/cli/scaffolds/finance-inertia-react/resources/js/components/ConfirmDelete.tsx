import { router } from "@inertiajs/react";
import { type ReactNode, useState } from "react";

import { useFinanceI18n } from "../lib/i18n";
import { Button } from "./ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "./ui/dialog";

/** Asks before a destructive request: the trigger opens a dialog and only its confirm button sends the DELETE. */
export default function ConfirmDelete({ href, name, trigger }: { href: string; name: string; trigger: ReactNode }) {
  const { t } = useFinanceI18n();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("confirmDeleteTitle")}</DialogTitle>
          <DialogDescription>
            {name}. {t("confirmDeleteBody")}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setOpen(false)}>
            {t("cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={busy}
            onClick={() =>
              router.delete(href, {
                preserveScroll: true,
                onStart: () => setBusy(true),
                onFinish: () => {
                  setBusy(false);
                  setOpen(false);
                },
              })
            }
          >
            {t("delete")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
