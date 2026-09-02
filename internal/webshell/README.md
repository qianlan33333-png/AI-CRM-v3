# Webshell

`internal/webshell` owns the v3 presentation shell only:

- the complete admin navigation and shared base layout;
- the `/login` and `/auth/wecom/start` reserved login surfaces;
- the `/admin/config/login-access` reserved access entry;
- the `/sidebar/bind-mobile` WeCom sidebar shell and its frozen production data URL attributes;
- embedded CSS, navigation icons, and the donor sidebar cover asset.

The standalone `Handler` is suitable for `httptest` and local previews. It does
not authenticate, issue sessions, read business data, call business APIs, call
providers, or connect the Composition Root. Data URL attributes are declarative
only; the webshell does not register unimplemented sidebar API routes.

Presentation assets were copied verbatim from AI-CRM commit
`69c5282fb38058f2cc9872b6feb3f0f54bfad64b`.
