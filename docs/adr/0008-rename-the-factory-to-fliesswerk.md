# Rename the Factory from Kraftwerk to Fliesswerk before the first release

The Factory was called Kraftwerk (short command `kwk`, env var prefix `KRAFTWERK_`). "KRAFTWERK" is a registered trademark of a co-founder of the band, including an EU registration (IR 1813006). No live registration covers software (class 9 software, or class 42), but the band is famous; in the EU, a mark with a reputation can be protected outside its registered classes, and the owner has enforced the name against an unrelated product before. So the Factory is renamed to **Fliesswerk**: technical name `fliesswerk` (repo, Go module, binary, directories), short command `fwk`, env var prefix `FLIESSWERK_`. "Fliess" comes from *Fliessband* (assembly line), and a factory is a *Werk*. The name passed the trademark (DPMA, EUIPO via TMview, USPTO, WIPO), famous-brand, GitHub, Go module path and command-name checks recorded on [#75](https://github.com/Marcel-Goehn/fliesswerk/issues/75).

The rename happens before v0.1.0, when it costs one change in this repo. After a release it would break installs, the Go module path and every link. Everything that ships or that users type is renamed: the module path, the binary and its short alias, the `.fliesswerk/` project directory, the user config and log directories, every env var, command output, templates, prompts, the JSON Schema and the docs. There are no compatibility shims, because nothing was released.

## Considered Options

- **Keep the name with a prefix or suffix** ("kraftwerk-agents"): rejected, because "Kraftwerk" would stay the dominant part of the name.
- **Nachtwerk**: rejected, because an identical EU mark (018194867) covers downloadable software in class 9.
- **takt**, **nightshift**: rejected, because AI agent tools with the same name already exist.
- **Tagwerk, Stellwerk, Feinwerk, Triebwerk**: rejected, because they are everyday words, crowded on GitHub, and their domains are taken.

## Consequences

- ADRs 0001–0007 keep their text as written, so they still say `kraftwerk`, `KRAFTWERK_` and `kwk`. Read those as `fliesswerk`, `FLIESSWERK_` and `fwk`.
- The GitHub repo, its Actions secret and variable, the Spec and open Tickets are renamed outside this repo. Until the repo is renamed, the install scripts' and `update`'s download URLs, the issue links and the issue tracker named in `CLAUDE.md` point at a repo that doesn't exist yet.
- A trademark lawyer's opinion on whether a `-werk` name is too close to "Kraftwerk" hasn't been obtained.
