import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

/**
 * AgentDock 的 runtime API 只监听 127.0.0.1:8765 且没有 CORS。
 * 浏览器直连会被跨域拦，所以这里在 dev/preview server 里加一层本地代理：
 * 浏览器 -> /dashboard/api/* -> 127.0.0.1:8765/internal/runtime/*
 * 生产环境由 AgentDock Go core 提供同形代理；开发环境由这里的 Vite 中间件提供。
 * token 从本机 agentdock.env 读取后注入，绝不落到前端代码或产物里。
 *
 * 安全：AgentDock 的 network capture 会把 Authorization 等请求头原文记进
 * records 里。代理层对所有响应 JSON 做递归脱敏，避免把凭据暴露到浏览器。
 */
function readConfig() {
  const envPath =
    process.env.AGENTDOCK_ENV_FILE ||
    path.join(os.homedir(), 'Library/Application Support/AgentDock/agentdock.env');
  let token = process.env.AGENTDOCK_AUTH_TOKEN || '';
  let host = process.env.AGENTDOCK_HOST || '127.0.0.1';
  let port = process.env.AGENTDOCK_PORT || '8765';
  try {
    for (const raw of fs.readFileSync(envPath, 'utf8').split('\n')) {
      const line = raw.trim();
      const eq = line.indexOf('=');
      if (eq < 0 || line.startsWith('#')) continue;
      const key = line.slice(0, eq).trim();
      let value = line.slice(eq + 1).trim();
      value = value.replace(/^['"]|['"]$/g, '');
      if (key === 'AGENTDOCK_AUTH_TOKEN' && !process.env.AGENTDOCK_AUTH_TOKEN) token = value;
      if (key === 'AGENTDOCK_HOST' && !process.env.AGENTDOCK_HOST) host = value;
      if (key === 'AGENTDOCK_PORT' && !process.env.AGENTDOCK_PORT) port = value;
    }
  } catch {
    // env 文件不存在时退回默认值，连接失败会在页面上显示。
  }
  return { token, target: `http://${host}:${port}/internal/runtime` };
}

const SENSITIVE_HEADERS = /^(authorization|cookie|set-cookie|x-api-key|api-key|proxy-authorization)$/i;
const SENSITIVE_BODY_KEYS = /^(authorization|access_token|refresh_token|token|api[_-]?key|password|secret|cookie)$/i;
const REDACTED = '[REDACTED]';

/** 递归脱敏：请求头数组按 name 处理，对象按 key 处理，字符串兜底清理 Bearer。 */
function redact(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redact);
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      if (SENSITIVE_HEADERS.test(k) || SENSITIVE_BODY_KEYS.test(k)) {
        out[k] = REDACTED;
      } else if (k === 'name' && typeof v === 'string' && SENSITIVE_HEADERS.test(v)) {
        // 请求头数组里的 {"name":"Authorization","value":"..."}：保留名字，洗掉紧邻的 value
        out[k] = v;
      } else {
        out[k] = redact(v);
      }
    }
    // 处理 {"name":"Authorization","value":"Bearer xxx"} 这种成对结构
    if (typeof out.name === 'string' && SENSITIVE_HEADERS.test(out.name) && 'value' in out) {
      out.value = REDACTED;
    }
    return out;
  }
  if (typeof value === 'string') {
    return value.replace(/Bearer\s+[A-Za-z0-9._~+/=-]{8,}/gi, `Bearer ${REDACTED}`);
  }
  return value;
}

function sanitizeJSON(text: string): string {
  try {
    return JSON.stringify(redact(JSON.parse(text)));
  } catch {
    // 非 JSON（例如错误页）时用正则兜底
    return text.replace(/Bearer\s+[A-Za-z0-9._~+/=-]{8,}/gi, `Bearer ${REDACTED}`);
  }
}

function attach(server: { middlewares: { use: (p: string, h: (...a: any[]) => void) => void } }) {
  server.middlewares.use('/dashboard/api', async (req: any, res: any) => {
    const { token, target } = readConfig();
    const subPath = req.url || '';
    const url = `${target}${subPath}`;
    // 记录响应头是否已发出：SSE 流中途出错时不能再 setHeader，否则 dev server 崩溃。
    let headersSent = false;
    const fail = (err: any) => {
      if (headersSent || res.headersSent || res.writableEnded) {
        try { res.end(); } catch { /* ignore */ }
        return;
      }
      res.statusCode = 502;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.end(
        JSON.stringify({
          ok: false,
          code: 'UPSTREAM_UNAVAILABLE',
          error: `无法连接 AgentDock (${url}): ${err?.message || err}`,
        }),
      );
    };
    try {
      // 上游忙时可能长时间不回；给个总超时，避免请求无限挂着。
      const method = req.method || 'GET';
      const chunks: Buffer[] = [];
      if (method !== 'GET' && method !== 'HEAD') {
        for await (const chunk of req) {
          chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
        }
      }
      const headers: Record<string, string> = {};
      if (token) headers.Authorization = `Bearer ${token}`;
      const contentType = req.headers['content-type'];
      if (typeof contentType === 'string' && contentType) headers['Content-Type'] = contentType;
      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(), 30_000);
      const upstream = await fetch(url, {
        method,
        headers,
        body: chunks.length ? Buffer.concat(chunks) : undefined,
        signal: ctrl.signal,
      });

      // SSE 事件流：保持流式透传（事件本身是摘要投影，不含凭据）。
      const ct = upstream.headers.get('content-type') || '';
      if (ct.includes('text/event-stream')) {
        res.statusCode = upstream.status;
        res.setHeader('Content-Type', ct);
        res.setHeader('Cache-Control', 'no-cache');
        res.setHeader('X-Accel-Buffering', 'no');
        headersSent = true;
        const reader = upstream.body?.getReader();
        if (!reader) { clearTimeout(timer); return res.end(); }
        const decoder = new TextDecoder();
        try {
          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            res.write(decoder.decode(value, { stream: true }));
          }
        } catch {
          // 流中断（上游重启/超时）：头已发出，只能安静结束，不能再写错误头。
        } finally {
          clearTimeout(timer);
          try { res.end(); } catch { /* ignore */ }
        }
        return;
      }

      clearTimeout(timer);
      const text = await upstream.text();
      res.statusCode = upstream.status;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.setHeader('Cache-Control', 'no-store');
      headersSent = true;
      res.end(sanitizeJSON(text));
    } catch (err: any) {
      fail(err);
    }
  });
}

export default defineConfig(({ command }) => ({
  // 生产构建挂到 AgentDock Core 的 /dashboard/ 下；开发时页面在根路径。
  base: command === 'build' ? '/dashboard/' : '/',
  plugins: [
    react(),
    tailwindcss(),
    { name: 'agentdock-runtime-proxy', configureServer: attach, configurePreviewServer: attach },
  ],
  server: { host: '0.0.0.0', port: 5173 },
  preview: { host: '0.0.0.0', port: 4173 },
}));
