#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const [command, ...args] = process.argv.slice(2);
const python = process.env.OTOHA_PYTHON || (process.platform === 'win32' ? 'python' : 'python3');
const service = name => path.join(root, 'services/collaboration', name);
const routes = {
  graph: [process.execPath, [path.join(root, 'packages/graph/bin/otoha-graph.mjs'), ...args]],
  server: [python, [service('otoha_server.py'), ...args]],
  message: [python, [service('otoha.py'), ...args]],
  mcp: [python, [service('otoha_mcp.py'), ...args]],
  mailbox: [python, [path.join(root, 'integrations/mailbox/skills/otoha-mailbox/scripts/mailbox.py'), ...args]],
};

if (!command || ['help', '--help', '-h'].includes(command)) {
  console.log(`Otoha — 人与多 Agent 协作基础设施

  otoha graph <command>     图谱、查询、看板与本地预览
  otoha server <command>    协作服务 init / serve / check / backup
  otoha message <options>   消息、身份、查询与回执
  otoha mcp <options>       本地 stdio MCP 适配器
  otoha client <command>    原生 CLI、项目接入与后台接收
  otoha mailbox <command>   GitHub 收信工具

源码使用：node bin/otoha.mjs <command>
图谱依赖：npm run setup
原生 CLI：npm run build:cli（需要 Go 1.26+）
OTOHA_PYTHON 可指定 Python 3.10+ 解释器。`);
} else {
  let target = routes[command];
  if (command === 'client') {
    const executable = path.join(root, 'dist', process.platform === 'win32' ? 'otoha.exe' : 'otoha');
    if (!fs.existsSync(executable)) {
      console.error('Otoha 原生 CLI 尚未构建；先运行 npm run build:cli。');
      process.exit(2);
    }
    target = [executable, args];
  }
  if (!target) {
    console.error(`未知命令 ${command}；运行 otoha help。`);
    process.exit(2);
  }
  // Keep the caller's working directory: graph paths and mailbox origin belong
  // to the user's project, not to this source checkout.
  const child = spawn(target[0], target[1], { stdio: 'inherit' });
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
  child.on('error', error => { console.error(error.message); process.exitCode = 1; });
  child.on('exit', (code, signal) => { if (signal) process.exitCode = 1; else process.exitCode = code ?? 1; });
}
