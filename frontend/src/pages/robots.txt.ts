/**
 * robots.txt — prerendered. It has no request-time input, so there is no reason
 * to wake a worker for it.
 *
 * `Disallow: /admin` is a crawl instruction, not a security control: the admin
 * routes are protected by Cloudflare Access at the edge, and robots.txt is a public
 * file that an attacker reads first. It is here so the admin routes do not end
 * up in a search index, nothing more.
 */

import type { APIRoute } from 'astro';
import { absoluteUrl } from '@lib/urls';

export const prerender = true;

export const GET: APIRoute = ({ site }) => {
  const body = [
    'User-agent: *',
    'Allow: /',
    'Disallow: /admin',
    'Disallow: /admin/',
    '',
    `Sitemap: ${absoluteUrl('/sitemap.xml', site)}`,
    '',
  ].join('\n');

  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      'Cache-Control': 'public, max-age=3600',
    },
  });
};
