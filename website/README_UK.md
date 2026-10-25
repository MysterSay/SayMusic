# Say Music — сайт програми

Cloudflare Worker зі статичною сторінкою, трьома скриншотами та архівом розширення 1.5.35. Авторизацію Google повністю прибрано. Старі `/auth/google/*` та `/api/app-auth/*` повертають 410; `/login-app` перенаправляє на головну.

## Оновлення на Cloudflare

Розпакуй проєкт у нову папку та перенеси в нього свої `routes`/налаштування домену з поточного `wrangler.jsonc`, якщо вони були вказані у файлі. Ім’я Worker залишається `q7m4v9k2r8`.

```powershell
npm ci
npm run check
npm run build
npm run deploy
```

За потреби перед деплоєм: `npx wrangler login` у звичайному браузері свого ПК.

Папка `public` містить сайт та зображення. Деплой завантажує їх разом із Worker.

Міграція `auth-removed-v2` видаляє старе сховище одноразових запитів авторизації `AuthFlow`. Попередні міграції збережено для коректного оновлення наявного Worker. Binding `AUTH_FLOWS` видалено.

Секрети Google більше не використовуються. Після успішного деплою їх можна видалити:

```powershell
npx wrangler secret delete GOOGLE_CLIENT_ID
npx wrangler secret delete GOOGLE_CLIENT_SECRET
npx wrangler secret delete GOOGLE_REDIRECT_URI
```

Видалення секретів не потрібне для роботи сайту.
