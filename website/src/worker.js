const headers = {
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
  'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'none'",
  'Permissions-Policy': 'camera=(), microphone=(), geolocation=()',
};
export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname.startsWith('/auth/') || url.pathname.startsWith('/api/app-auth/')) {
      return new Response('Авторизацію на сайті видалено.', {status:410, headers});
    }
    if (!['GET','HEAD'].includes(request.method)) return new Response(null,{status:405,headers:{...headers,Allow:'GET, HEAD'}});
    if (['/login-app','/login-app/'].includes(url.pathname)) return new Response(null,{status:308,headers:{...headers,Location:'/'}});
    if (url.pathname === '/health') return Response.json({status:'ok'},{headers});
    const response = await env.ASSETS.fetch(request);
    const result = new Response(response.body,response);
    for (const [name,value] of Object.entries(headers)) result.headers.set(name,value);
    return result;
  }
};
