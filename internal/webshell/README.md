# Webshell

`internal/webshell` owns the v3 presentation shell only:

- the complete admin navigation and shared base layout;
- the `/login` and `/auth/wecom/start` reserved login surfaces;
- the `/admin/config/login-access` reserved access entry;
- the `/sidebar/bind-mobile` WeCom sidebar shell and its frozen bootstrap data URL attributes;
- embedded CSS, navigation icons, and the donor sidebar cover asset.

The standalone `Handler` is suitable for `httptest` and local previews. It does
not authenticate, issue sessions, read business data, call providers, or
connect the Composition Root. The rendered Access and sidebar pages call only
their domain-owned endpoints when mounted by a composition root; the shell
does not register those API routes itself. Unimplemented sidebar tabs remain
local empty states.

Presentation assets were copied verbatim from AI-CRM commit
`69c5282fb38058f2cc9872b6feb3f0f54bfad64b`.
