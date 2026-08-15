import { shellCopy, type SupportedLanguage } from "./localization";

interface AppProps {
  language: SupportedLanguage;
}

export function App({ language }: AppProps) {
  const copy = shellCopy[language];

  return (
    <main className="shell" data-devhud-ready="true">
      <p className="eyebrow">{copy.eyebrow}</p>
      <h1>DevHUD</h1>
      <p className="summary">{copy.summary}</p>
    </main>
  );
}
