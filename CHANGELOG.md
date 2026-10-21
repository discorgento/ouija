# Changelog

## [0.6.0](https://github.com/discorgento/ouija/compare/v0.5.1...v0.6.0) (2026-10-21)


### ⚠ BREAKING CHANGES

* **sandbox:** with tools.bash.sandbox enabled, commands that use the network (downloading dependencies, calling a local service) now need network.connect, e.g. [443], or network.all.

### Features

* **doctor:** report whether bash runs in the sandbox ([9ec02cd](https://github.com/discorgento/ouija/commit/9ec02cdbbf40d690ec4383b8fc8ae7c5da40c433))
* **install:** verify the archive's build provenance ([842cb9f](https://github.com/discorgento/ouija/commit/842cb9f1c294784e4275d1d613898cb61d02e9df))
* **mcp:** annotate read-only totems for MCP hosts ([3fec9e7](https://github.com/discorgento/ouija/commit/3fec9e7fddf4100ecc8fe9c1359a45fee08aa191))
* **sandbox:** a global sandbox no project can turn off ([77afc81](https://github.com/discorgento/ouija/commit/77afc8165f7b48253673512aa28bf03b48121c0d))
* **sandbox:** close TCP by default under the bash sandbox ([d682c47](https://github.com/discorgento/ouija/commit/d682c47aa9c05027e60b8c2865908e2dc036692c))

## [0.5.1](https://github.com/discorgento/ouija/compare/v0.5.0...v0.5.1) (2026-10-21)


### Documentation

* **install:** lead with the one-command install and show it ([e468696](https://github.com/discorgento/ouija/commit/e468696778db668f4c02af9af45aef8ca4380d30))

## [0.5.0](https://github.com/discorgento/ouija/compare/v0.4.0...v0.5.0) (2026-10-21)


### Features

* **install:** offer to fix PATH and run setup after installing ([04746bb](https://github.com/discorgento/ouija/commit/04746bb51ef5f542fb1cd9ce4bab936f7ba4b5db))
* **setup:** a three-step wizard that checks the key ([a6a906e](https://github.com/discorgento/ouija/commit/a6a906ed666d3d05aff857efc60ae10954b2c5fc))
* **setup:** open the wizards with the planchette spelling the name ([6d64a36](https://github.com/discorgento/ouija/commit/6d64a366a0374aa99272f7037a9f6896fd2c7fb9))
* **setup:** search a long model list as you type, free models first ([30d3b1e](https://github.com/discorgento/ouija/commit/30d3b1e14a923dd848d586b7a7b5e6197aed862b))

## [0.4.0](https://github.com/discorgento/ouija/compare/v0.3.0...v0.4.0) (2026-10-20)


### Features

* **review:** add a review totem for the branch's changes ([938beb5](https://github.com/discorgento/ouija/commit/938beb5d380d6e41e1db944abdc527ae78b028ca))
* **site:** show the review totem as the write-your-own example ([14b8382](https://github.com/discorgento/ouija/commit/14b8382f925c770d861f4edb1c84f4742a0bab8d))


### Bug Fixes

* **mcp:** never answer a request the client cancelled, even during shutdown ([694c813](https://github.com/discorgento/ouija/commit/694c8134e9488044a9c5c5c698fb0d0604b600b8))


### Documentation

* **assets:** show doctor, an approved fix and /review in the demo ([007b4fa](https://github.com/discorgento/ouija/commit/007b4fade8452a16b7ab5b25c8223c6a81e81322))
* rewrite the prose so a newcomer understands it on first read ([a9fbc81](https://github.com/discorgento/ouija/commit/a9fbc81d76758459d8611c4a3d40441644a18a2a))
* **totems:** teach writing a totem with the review example ([d17a07e](https://github.com/discorgento/ouija/commit/d17a07ec230a70b4e982d4eecaaf7096f5baa877))

## [0.3.0](https://github.com/discorgento/ouija/compare/v0.2.0...v0.3.0) (2026-10-20)


### Features

* **install:** add install.sh for Linux and macOS ([1dd1d71](https://github.com/discorgento/ouija/commit/1dd1d719bfbe1111e92bec5c186659d9c723d43c))
* **site:** add a live terminal demo and animate the séance ([27f8f32](https://github.com/discorgento/ouija/commit/27f8f321cf480c014d5c2be74757f2d87ae1833b))
* **site:** add a terminal-styled landing page published to GitHub Pages ([733fcb8](https://github.com/discorgento/ouija/commit/733fcb8f604ee6942ed7e9010b03a25b5f54eee1))
* **site:** compare a prompt with a totem, add a first-five-minutes guide and a light theme ([4c53dc6](https://github.com/discorgento/ouija/commit/4c53dc6d8e4b2eafb0fb982214a615147c644d6d))


### Bug Fixes

* **install:** fall back to the newest release that has its Windows archive ([17ff28f](https://github.com/discorgento/ouija/commit/17ff28fd8c9afc1367d410918b82138433c6c3a7))

## [0.2.0](https://github.com/discorgento/ouija/compare/v0.1.0...v0.2.0) (2026-10-20)


### Features

* **release:** name the archives after the version, zip for Windows, add install.ps1 ([63f3af9](https://github.com/discorgento/ouija/commit/63f3af9dfe77d801256204a350db47550df3e72e))

## 0.1.0 (2026-10-20)


### ⚠ BREAKING CHANGES

* **config:** DefaultConfig has no provider or model, and LoadConfig no longer requires them; run Validate on the merged config.
* **registry:** Totem no longer includes Init/Cleanup; implement Initializer (Init(ctx, eng)) and Cleaner instead, and run totems with registry.Invoke.
* **config:** Config.Passes and Config.Dimensions are removed; use Config.Totems / TotemConfig.
* **engine:** Agent.Run takes a RunEnv; NewReAct takes no config and ReActConfig is gone; PermissionConfig must be changed through SetTool / SetDefault once handed to an Engine.

### Features

* a purple planchette logo, built on the original design ([edee6f4](https://github.com/discorgento/ouija/commit/edee6f4640ba49a454cdf2bb06e9cc63a734e177))
* **bootstrap:** expose config-to-engine wiring for external totems ([8853598](https://github.com/discorgento/ouija/commit/8853598454a3dfebd9b068a31a4b5bb936e2f475))
* **bootstrap:** run a totem binary from one line with bootstrap.Main ([993f6e1](https://github.com/discorgento/ouija/commit/993f6e1ab2f2da6e1ed19bb8c9e9dcd8d4974c49))
* **chat:** name the totems on the welcome again ([f218297](https://github.com/discorgento/ouija/commit/f21829715883936253a2d08e01dd97f51bd7b3a0))
* **chat:** redesign the TUI — welcome, tools, diffs, approvals, search ([e15f34c](https://github.com/discorgento/ouija/commit/e15f34cb8015b4cac77a52bafbd69bf893e5e2a4))
* **chat:** run every totem, /learn, command output and golden screens ([bb40b5c](https://github.com/discorgento/ouija/commit/bb40b5caf88c7ebec2ff05a1dd84263c39562b01))
* **chat:** smooth streaming, `@file` mentions, /undo, /task, sessions and themes ([4b64c28](https://github.com/discorgento/ouija/commit/4b64c28875ccdaec799143f58bcb0bb4cef5286b))
* **chat:** trim the welcome to the logo and four places to start ([ff1be74](https://github.com/discorgento/ouija/commit/ff1be748cea5dbf09274a49263edfe49232c340a))
* **cli:** add a shared palette and restyle doctor, setup and init ([e66bcd8](https://github.com/discorgento/ouija/commit/e66bcd83e9b49aa10a7eb8be53d2d254ec741612))
* **cli:** draw totem runs live on the terminal ([ab58dbb](https://github.com/discorgento/ouija/commit/ab58dbb2d10842602c79c41af1d666a2c8e86341))
* **config:** require the developer to choose the provider and model ([bd20015](https://github.com/discorgento/ouija/commit/bd200150b8892051bae9d2a60df1a72e00809dec))
* **config:** require trust before applying a discovered project config ([38482fb](https://github.com/discorgento/ouija/commit/38482fbb96fbf1cfb2b2c7e10ca048437cf5e199))
* **engine:** add an undo journal and project file listing ([d8c2374](https://github.com/discorgento/ouija/commit/d8c23747efd8c4ef3c18f0ea7679b19de478ef51))
* **engine:** confine bash commands with Linux Landlock (opt-in) ([df96861](https://github.com/discorgento/ouija/commit/df9686112b4f96b5aca5fd5f3241eeccdd65427b))
* **engine:** fit tool results and history to the model's context window ([fdea40d](https://github.com/discorgento/ouija/commit/fdea40d1c74d90d358d9c4a7d7f29f25cafd8580))
* **engine:** report run start/end, turn tool summaries and context use ([72d1b10](https://github.com/discorgento/ouija/commit/72d1b10f634de400b11ecd39f6b6e6584db6c7a2))
* **generate:** scaffold standalone external totem modules ([5f7daac](https://github.com/discorgento/ouija/commit/5f7daaca2f75fa49ae3e78cc093bd249c7617a14))
* **harness:** add CLI, MCP server, engine, providers and totems ([852ae3d](https://github.com/discorgento/ouija/commit/852ae3da17c2e94ea30c4a6ee87cc5a8c90d2cc4))
* **mcp:** run tool calls concurrently and support cancellation ([5833612](https://github.com/discorgento/ouija/commit/583361294abee894acb815ef8b35f23e86ce0b0e))
* **models:** list each provider's models from its own API ([9f6186c](https://github.com/discorgento/ouija/commit/9f6186c55636a08b57fa60e4f4378c17ddd83ee8))
* **registry:** make the totem lifecycle optional and add typed inputs ([6c1c5b7](https://github.com/discorgento/ouija/commit/6c1c5b717a5bc5f59fb8d9b6f683d15886a98be7))
* **sticker:** mark the sticker with the new planchette ([2eeb850](https://github.com/discorgento/ouija/commit/2eeb850aa501ec9a6f447aa900dfcaf26dbcfe68))
* take the context window from the config, the model lists or Ollama's default ([8e314fc](https://github.com/discorgento/ouija/commit/8e314fc0149dcc5c0c2a38663d445ed73c07260c))
* **theme:** add catppuccin, dracula and mono themes ([5245d54](https://github.com/discorgento/ouija/commit/5245d548cefe2da39cb24665e737c2fe6c818731))
* **theme:** add OUIJA_NO_ANIMATION for a still screen ([c6f3d55](https://github.com/discorgento/ouija/commit/c6f3d5567f5e16df989b83de966430d6c013f075))
* **theme:** move ouija's palette from pink toward purple ([7609d13](https://github.com/discorgento/ouija/commit/7609d13ff63a62abe2d40c74f270a7f6c2266870))
* **trace:** mark turns instead of resetting the tracer ([cc18514](https://github.com/discorgento/ouija/commit/cc185141ec21637355c665dbfdfc14a24be33e95))
* **trace:** show the grep/glob pattern next to the path ([c9e5807](https://github.com/discorgento/ouija/commit/c9e580726f921c9a5135bdb507bd70569d9e9c04))


### Bug Fixes

* **bash:** judge expanded words in the denylist and keep failing output ([85c205a](https://github.com/discorgento/ouija/commit/85c205ac552d6cdb4c6ae3e61be802c501629f4c))
* **bash:** stop learned patterns from pre-approving extended commands ([208c0a5](https://github.com/discorgento/ouija/commit/208c0a56204606b8d7b774e8bb78d227d8f9835e))
* **build:** pass ldflags once in make install and trim release paths ([52f47dc](https://github.com/discorgento/ouija/commit/52f47dc9a76fecda49d7230a0578e0fd3b60c4b6))
* **chat:** fetch model lists off the event loop, keep text terminal-safe ([cf824a8](https://github.com/discorgento/ouija/commit/cf824a8d6fc3360e17056cddfd744cc72a515090))
* **chat:** keep the session trace and match approval keys to the menu ([65d6ccd](https://github.com/discorgento/ouija/commit/65d6ccd5816a524b241930ff53c803e4b10633f3))
* **chat:** send slash commands as typed and fix approval and queue flow ([5ff153a](https://github.com/discorgento/ouija/commit/5ff153a051e975a00f623ddec8cac11a62cdb0a7))
* **chat:** send the assembled system prompt and unify generation handling ([7c4ad1f](https://github.com/discorgento/ouija/commit/7c4ad1f5fc5b5dcce7b6091d0f90056042cfd056))
* **chat:** show the checkboxes of a task list ([f8b385c](https://github.com/discorgento/ouija/commit/f8b385cd60bd51932e4a953b21af3f812dba5a83))
* **chat:** Windows key releases, editor default and config writes ([6f26e15](https://github.com/discorgento/ouija/commit/6f26e15ecf7b2e730e3871bb2610d6733f74d807))
* **cli:** make --help safe, exit codes consistent and first-run hints right ([66ee37f](https://github.com/discorgento/ouija/commit/66ee37fdcf45022c6526dbb4e52d802c4af2f4be))
* **cli:** reject stray totem args and surface broken config errors ([2415db7](https://github.com/discorgento/ouija/commit/2415db73a39a1ac385fa542ee264d191f07ed7c7))
* **cli:** stop listing the task totem twice in help ([1a06630](https://github.com/discorgento/ouija/commit/1a066301674c69381d71221681af89c312f23bc1))
* **cli:** strip terminal controls from repo and model text; check the key ([8698e69](https://github.com/discorgento/ouija/commit/8698e6943d507b5e4e75a52f7cded43c857c8834))
* **config:** harden project trust and never overwrite a broken config ([99fb4e3](https://github.com/discorgento/ouija/commit/99fb4e33015f3fe6d7024a4a051a34d15b1acdd6))
* **config:** keep the sandbox root inside the project ([60859ea](https://github.com/discorgento/ouija/commit/60859ea9774c4cdc0a68839d74e3c8cb4de2bcfe))
* **config:** scope the project API key to its provider ([b62fb49](https://github.com/discorgento/ouija/commit/b62fb49f05abe25ffa07b8f9f777109527a671f7))
* **deps:** build with Go 1.26.9 for 27 standard-library vulnerabilities ([fc8ef01](https://github.com/discorgento/ouija/commit/fc8ef01bdacbb69d1a518a180702ca446af3f096))
* **doc:** lock the generated HTML down with a Content-Security-Policy ([01b3023](https://github.com/discorgento/ouija/commit/01b3023b7fa1921af4260d43daaee136c5f34998))
* **doc:** put the CSP before any model-written markup ([40a5134](https://github.com/discorgento/ouija/commit/40a51341d4ce6b45fe199dd45a5333e64227c613))
* **engine:** bound provider streams by silence, not length, and retry dropped ones ([289ff54](https://github.com/discorgento/ouija/commit/289ff5411b052970467ad318e60eaee73426abed))
* **engine:** classify stream errors and harden the tool pipeline ([de5d87b](https://github.com/discorgento/ouija/commit/de5d87b3fe84400ba9844eea6d460078939c30d1))
* **engine:** close sandbox escapes through dangling symlinks ([2ccc5bf](https://github.com/discorgento/ouija/commit/2ccc5bf1c705a5dea3e9ff3bf6324398c9eac081))
* **engine:** close Windows sandbox escapes and run bash in a real shell ([19ae53a](https://github.com/discorgento/ouija/commit/19ae53a1db23231278b03b9c34fedcdfc7fdfad5))
* **engine:** enforce protected paths through symlinks ([7c3fe4d](https://github.com/discorgento/ouija/commit/7c3fe4dbc033d63449c85844b3ab4bd34470fc6a))
* **engine:** guard protected paths and the home dir on Windows too ([69199c1](https://github.com/discorgento/ouija/commit/69199c129a52a81a4295a37e37862d339368117e))
* **engine:** judge bash path words by value and physical location ([66ae8e1](https://github.com/discorgento/ouija/commit/66ae8e1222e4459f080f1a682a6cd07f4d71ded4))
* **engine:** keep tool order, count cache tokens and stop on cancel ([9e09a99](https://github.com/discorgento/ouija/commit/9e09a99a541870730cafbdf4a00575ad0525d817))
* **engine:** make structured output and long chats reliable ([6dea3e9](https://github.com/discorgento/ouija/commit/6dea3e9e3881ecfc441e42df34b68c1b36c003ae))
* **engine:** make the file tools work on large repositories ([442c3f6](https://github.com/discorgento/ouija/commit/442c3f69715fdf38d2596b91f1298091a0adad8d))
* **engine:** mark a failing bash command failed and keep its output ([ab0abbb](https://github.com/discorgento/ouija/commit/ab0abbbc8da7ec51f41040b77999f9eca621673c))
* **engine:** never copy the resolved API key into a project config ([bcbe8b7](https://github.com/discorgento/ouija/commit/bcbe8b7059fe0e6a20521eab1749c92a512d5fe8))
* **engine:** offer the model only the tools the gate could run ([004727a](https://github.com/discorgento/ouija/commit/004727ae8703a349af0cdc44204feb5a08f8924f))
* **engine:** protect ouija's global config and project config files ([68969e2](https://github.com/discorgento/ouija/commit/68969e2987f6edf1397e8bdfdd4883ad7ad90197))
* **engine:** refuse cut-off structured answers, flag git hook changes ([39d74df](https://github.com/discorgento/ouija/commit/39d74df6ac04942ad117b5f9e8c8a6dec955d718))
* **engine:** require every simple command to match the bash allowlist ([b0a90ac](https://github.com/discorgento/ouija/commit/b0a90accabb1b47dbb866d88a91a13abe72d3781))
* **engine:** stop the bash path scan from panicking and judge globs and $HOME ([fce92d0](https://github.com/discorgento/ouija/commit/fce92d0c00775da43578ff6639cc240f16dd88f0))
* **generate:** anchor generated .gitignore entries ([823473b](https://github.com/discorgento/ouija/commit/823473b97383094c79daa6056b1e13fb3b009ff7))
* **generate:** pin ouija only for real releases in the external scaffold ([b15c316](https://github.com/discorgento/ouija/commit/b15c3167efba7f489737b3507945c8f747c59ae8))
* **generate:** reject unbuildable totem names and roll back failures ([3586c61](https://github.com/discorgento/ouija/commit/3586c613221630e3b8ac00c3cbb259493adbf813))
* **learn:** keep the project's knowledge at its root ([7cb3c08](https://github.com/discorgento/ouija/commit/7cb3c08efc762bd8a30ed0d2b7ea5befd0789ca0))
* **mcp:** do not panic installing into a config with "mcp": null ([f0e4015](https://github.com/discorgento/ouija/commit/f0e4015541712f9bf605fd635ed54bfad25269d5))
* **mcp:** honour an explicit config path in tool calls ([ddc83b1](https://github.com/discorgento/ouija/commit/ddc83b1636fa15e84394d49d7d4f76f83e997486))
* **mcp:** stop on signals, ignore notifications, harden resources/read ([daceee7](https://github.com/discorgento/ouija/commit/daceee720031970064c12b055b33a6ff883fc618))
* **models:** scope the list cache and offer only chat models ([3ebbc30](https://github.com/discorgento/ouija/commit/3ebbc3007d698f87a005a0184ff9f3f73ee19487))
* **models:** stop advertising OpenCode's free tier ([540ea66](https://github.com/discorgento/ouija/commit/540ea66da55c14ea2e409335aa6748199232f88e))
* **providers:** assemble Anthropic tool input and harden streaming ([12174ba](https://github.com/discorgento/ouija/commit/12174ba684b807fb5f908c815726bccca4b5f936))
* **providers:** cap the server-suggested Retry-After wait ([1f90aea](https://github.com/discorgento/ouija/commit/1f90aea00134215450c2996bfa885a48cbaa2932))
* **providers:** emit OpenAI tool calls in order and classify failures ([49194fe](https://github.com/discorgento/ouija/commit/49194fe2541e5a2ba1bc8c17c1c515bd8735a42e))
* **providers:** report refused keys as auth errors with an actionable hint ([eb8e07f](https://github.com/discorgento/ouija/commit/eb8e07fc52a9be8fa3a67aa7dab3fef036d03689))
* **providers:** use native structured outputs on Anthropic and declare tools in the final call ([e0b7770](https://github.com/discorgento/ouija/commit/e0b77707310a93b2faaa96aea471b5d3999d0e0f))
* **registry:** make Invoke safe for concurrent hosts and type --set inputs ([10870ce](https://github.com/discorgento/ouija/commit/10870ce689dbea3bca78f9c8df01692e40a5c6fc))
* resolve totem outputs and knowledge through the sandbox ([a567918](https://github.com/discorgento/ouija/commit/a567918c45f4e4235de40f2c11f3014b171ab821))
* **sandbox:** require Landlock ABI 9, confine scopes, guard grants ([aa1f3bb](https://github.com/discorgento/ouija/commit/aa1f3bb9f2eb4a6ae5dad3695385a9bcf5b4bfbb))
* **setup:** keep trusted projects and unknown keys in the global config ([1a79389](https://github.com/discorgento/ouija/commit/1a79389a7edadd85662b92b914b995d90c9d23ca))
* **task:** read knowledge and write the plan under the project root ([3c922fc](https://github.com/discorgento/ouija/commit/3c922fc88813460f7571e6a679bec153d8983dd5))
* **totems:** escape sticker SVG, never overwrite user docs, bound PDF ([6d7bf36](https://github.com/discorgento/ouija/commit/6d7bf36a536d3dd501534fa56b7a17365f0a9624))
* **totems:** never save an empty plan, fill the doc cover, fix sticker names ([f6ee8f8](https://github.com/discorgento/ouija/commit/f6ee8f81d11cb9cfa989266ccb1dc4dc6213a7f0))
* write config files atomically ([31d63b4](https://github.com/discorgento/ouija/commit/31d63b40f4a2a626e715b16bd2aadbce52b6a54c))


### Documentation

* **architecture:** show the public packages in the layer tree ([1e8df07](https://github.com/discorgento/ouija/commit/1e8df07add1640db9b406caa8c338f8e333fc2b3))
* document git guard, typed --set, totem concurrency and doc cover keys ([7ca3703](https://github.com/discorgento/ouija/commit/7ca370358dd531b5121e273092215c2ad1b95a8f))
* document trust, allowlist and sandbox boundaries ([3f3c4a2](https://github.com/discorgento/ouija/commit/3f3c4a257a0e0eab481da6f5534d5d2e3f5c8dd6))
* fix first-run docs (config paths per OS, flags, versions, trust keys) ([b20ef2f](https://github.com/discorgento/ouija/commit/b20ef2f00931737f594f3129d3dc6d75f3ec583c))
* invite totem contributions and show the ecosystem roadmap ([aabe3a0](https://github.com/discorgento/ouija/commit/aabe3a0835bac380fda8d5a9f917682c5e9480d6))
* make totems the lead story for developers ([531678b](https://github.com/discorgento/ouija/commit/531678b04f6ae7d6113fa24e5a465ec81d17f4db))
* new logo and banner, and a demo GIF in the README ([57175a0](https://github.com/discorgento/ouija/commit/57175a0d292a4d07c08ab99805c60df9e14ec4aa))
* pitch own-agent capability in the README hero ([8783ad2](https://github.com/discorgento/ouija/commit/8783ad2a4ed539011ba1c1216463fd289437677a))
* **readme:** refresh the screenshots and the GIF for the new welcome ([e4dc2c0](https://github.com/discorgento/ouija/commit/e4dc2c0410755f9b168f78331e381ccd7820135a))
* **readme:** refresh the screenshots and the GIF in the purple palette ([ac072ab](https://github.com/discorgento/ouija/commit/ac072ab86420ddfc109042bba9d03c1a3f77139d))
* **readme:** show the chat with screenshots ([e694907](https://github.com/discorgento/ouija/commit/e6949079c3363c35418402ccb4995129d4715101))
* resolve totem writes with SafeResolve and document enginetest ([f170861](https://github.com/discorgento/ouija/commit/f170861565e7cd2978f172cba325f46184eb1952))
* restructure into Diátaxis and rewrite root guides ([355f16f](https://github.com/discorgento/ouija/commit/355f16fde2ab8f2c07e8f6547ae1c6cff1dd1004))
* restructure README for a developer's first visit ([bc3a44d](https://github.com/discorgento/ouija/commit/bc3a44d4334c018a49b9053e77fcf15094798e2e))
* show chat as daily driver in quick start ([e5b02dc](https://github.com/discorgento/ouija/commit/e5b02dcdef940c66c9ff16630159378e3b6fed11))


### Refactoring

* **config:** move totem settings into per-totem config sections ([e7cf5ab](https://github.com/discorgento/ouija/commit/e7cf5abe22bb60cc9aeb6660c582167b734a1d04))
* **engine:** snapshot each run's environment and make the Engine safe for concurrent use ([2310a69](https://github.com/discorgento/ouija/commit/2310a694d9d95fab314218a02a3876fb45ac8057))

## Changelog

All notable changes to ouija are documented here.

This file is **generated** by [release-please](https://github.com/googleapis/release-please)
from [Conventional Commits](https://www.conventionalcommits.org/) — do not edit it
by hand. Each section below is also the release notes of the matching
[GitHub Release](https://github.com/discorgento/ouija/releases); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) and reach the binaries
through the git tag injected at link time (see `make help`).
