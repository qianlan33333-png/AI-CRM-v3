# Webshell

`internal/webshell` owns the v3 presentation shell only:

- the complete admin navigation and shared base layout;
- the `/login` and `/auth/wecom/start` reserved login surfaces;
- the `/admin/config/login-access` reserved access entry;
- the `/sidebar/bind-mobile` WeCom sidebar shell and its future v3 data URL attributes;
- embedded CSS, navigation icons, and the donor sidebar cover asset.

The standalone `Handler` is suitable for `httptest` and local previews. It does
not authenticate, issue sessions, read business data, call old APIs, call
providers, or connect the Composition Root. Reserved API paths return a
controlled `not_implemented` response until their domain owners are mounted.

Presentation assets were copied verbatim from AI-CRM commit
`69c5282fb38058f2cc9872b6feb3f0f54bfad64b`.
