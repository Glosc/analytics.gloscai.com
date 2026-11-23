import { Button } from "@components/Button";
import { IconCube } from "@tabler/icons-react";
import { useRef, useState } from "react";
import { AppIcon } from "./AppIcon";
import { useI18n } from "@i18n/index";

type ImageDetails = {
  src: string;
  contentAsBase64: string;
  width: number;
  height: number;
};

async function getImageDetails(file: File): Promise<ImageDetails> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.readAsDataURL(file);
    reader.onload = function (e) {
      var image = new Image();
      image!.src = reader.result as string;
      image.onload = function () {
        if (image.width !== image.height) {
          reject("Icon must have a 1:1 aspect ratio.");
          return;
        }

        if (image.width < 100 || image.height < 100) {
          reject("Icon must be at least 100x100.");
          return;
        }

        resolve({
          src: image.src,
          contentAsBase64: image.src.split(",")[1],
          width: image.width,
          height: image.height,
        });
      };
      image.onerror = function () {
        reject("Invalid image file, try a different one.");
      };
    };
    reader.onerror = function (error) {
      reject("Invalid image file, try a different one.");
    };
  });
}

type Props = {
  iconPath: string;
  onIconChanged: (contentAsBase64: string) => void;
};

export function AppIconUpload(props: Props) {
  const { t } = useI18n();
  const [imgSrc, setImgSrc] = useState<string>("");
  const [error, setError] = useState<string>("");
  const inputRef = useRef<HTMLInputElement>(null);

  function handleClick() {
    inputRef.current?.click();
  }

  async function handleFileChanged(event: React.ChangeEvent<HTMLInputElement>) {
    props.onIconChanged("");
    setError("");

    const file = event.target.files?.[0];
    if (!file) return;

    if (file.size > 1024 * 50) {
      setError(t("Icon size must be lower than 50KB."));
      return;
    }

    if (file.type !== "image/png") {
      setError(t("Icon must be a PNG file."));
      return;
    }

    try {
      const details = await getImageDetails(file);
      setImgSrc(details.src);
      props.onIconChanged(details.contentAsBase64);
    } catch (err: any) {
      console.log(err);
      const message = err instanceof Error ? err.message : err.toString();
      setError(typeof err === "string" ? t(err) : message);
    }
  }

  return (
    <div>
      <label className="text-sm mb-2 block font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70">
        {t("Icon")}
      </label>
      <div className="flex gap-2 items-center">
        {imgSrc ? (
          <img src={imgSrc} className="w-9 h-9 rounded" loading="lazy" />
        ) : props.iconPath ? (
          <AppIcon iconPath={props.iconPath} className="w-9 h-9" />
        ) : (
          <IconCube className="w-9 h-9 border p-1.5 rounded" />
        )}

        <input ref={inputRef} onChange={handleFileChanged} type="file" className="hidden" />
        <div>
          <Button variant="ghost" onClick={handleClick} type="button">
            {t("Change")}
          </Button>
        </div>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}
    </div>
  );
}
