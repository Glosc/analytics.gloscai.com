import { Application, useApps } from "@features/apps";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "@components/Button";
import { useT } from "@i18n/index";

type Props = {
  app: Application;
};

export function DangerZone(props: Props) {
  const t = useT();
  const { deleteApp } = useApps();
  const navigate = useNavigate();

  const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (window.confirm(t("Are you really sure you want to delete application '{name}'?", { name: props.app.name }))) {
      await deleteApp(props.app.id);
      toast(t("{name} app was deleted.", { name: props.app.name }));
      navigate("/");
    }
  };

  return (
    <form onSubmit={handleSubmit} className="border bg-card rounded p-4 max-w-md">
      <h2 className="text-lg">{t("Delete {name}?", { name: props.app.name })}</h2>
      <div className="text-sm text-muted-foreground">{t("This will permanently delete the app and its data.")}</div>
      <div className="mt-4">
        <div className="w-20">
          <Button variant="destructive">{t("Delete")}</Button>
        </div>
      </div>
    </form>
  );
}
