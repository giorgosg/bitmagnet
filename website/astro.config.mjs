import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

export default defineConfig({
  site: "https://giorgosg.github.io",
  base: "/magnes",
  integrations: [
    starlight({
      title: "Magnes",
      description:
        "A self-hosted BitTorrent indexer with an Elm interface, authentication, and Servarr integration.",
      favicon: "/favicon.svg",
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/giorgosg/magnes",
        },
      ],
      customCss: ["./src/styles/custom.css"],
      sidebar: [
        {
          label: "Start here",
          items: [
            "documentation",
            "getting-started/installation",
            "getting-started/first-login",
            "getting-started/from-bitmagnet",
          ],
        },
        {
          label: "Use Magnes",
          items: [
            "guides/interface",
            "guides/text-search",
            "guides/crawler-resources",
            "guides/classifier",
            "guides/reprocessing",
            "guides/import",
            "guides/monitoring",
            "guides/servarr",
            "guides/backup-upgrades",
            "guides/troubleshooting",
          ],
        },
        {
          label: "Configure",
          items: [
            "reference/configuration",
            "reference/authentication",
            "reference/endpoints",
          ],
        },
        { label: "Project", items: ["project/about", "project/contributing"] },
      ],
    }),
  ],
});
