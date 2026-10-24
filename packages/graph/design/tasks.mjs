import { matchesTaskFilter } from './filters.mjs';
import { parseModel, digest, problem } from './model.mjs';

export const taskFields = ['title', 'description', 'status', 'assignees', 'entities', 'acceptance', 'deliverables', 'blocked'];

// A shared projection for the browser and Agent queries. Task-to-module links
// are explicit associations, never dataflow, containment or evidence of maturity.
export function projectTasks(model, query = {}) {
  const tasks = (model.tasks || []).filter(task =>
    matchesTaskFilter(task, query.filter) &&
    (!query.target || task.id === query.target || task.entities.includes(query.target)) &&
    (!query.assignee || (query.assignee === '__unassigned__' ? !task.assignees.length : task.assignees.includes(query.assignee))) &&
    (!query.status || task.status === query.status) &&
    (!query.search || [task.title, task.description, ...task.assignees].join('\n').toLocaleLowerCase().includes(query.search.toLocaleLowerCase()))
  ).slice().sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  return { tasks, columns: ['todo', 'doing', 'review', 'done'].map(status => ({ status, taskIds: tasks.filter(t => t.status === status).map(t => t.id) })) };
}

export function changeTask(authority, data, transact) {
  if (!data || Object.keys(data).some(k => !['operationId', 'expectedCursor', 'action', 'task', 'taskId', 'patch', 'deletionId'].includes(k))) problem('task/argument', 'Unknown task command field');
  if (typeof data.operationId !== 'string' || !/^[a-zA-Z0-9_-]{1,100}$/.test(data.operationId)) problem('task/argument', 'Use a stable operationId');
  authority.writable();
  const payloadHash = digest(JSON.stringify(data)), duplicate = authority.records.find(r => r.operationId === data.operationId);
  if (duplicate) {
    if (duplicate.payloadHash !== payloadHash) problem('authority/idempotency-conflict', 'Operation ID has different content', {}, 409);
    return { ok: true, replayed: true, cursor: duplicate.cursor };
  }
  authority.refresh();
  if (authority.failure) problem('task/source-invalid', 'Repair the working source before editing tasks', authority.failure, 409);
  if (!Number.isInteger(data.expectedCursor) || data.expectedCursor !== authority.record().cursor) problem('task/conflict', 'The accepted version changed. Your draft must be reviewed before retrying.', authority.status(), 409);
  const old = authority.loadObject(authority.record().object), model = structuredClone(parseModel(old.source).model);
  model.tasks ||= [];
  if (data.action !== 'restore' && data.deletionId !== undefined) problem('task/argument', 'Only restore accepts a deletionId');
  let taskId = data.task?.id || data.taskId;
  if (data.action === 'create') {
    if (data.taskId !== undefined || data.patch !== undefined || !data.task) problem('task/argument', 'Create requires one complete task');
    if (model.tasks.some(t => t.id === data.task.id)) problem('task/conflict', 'Task ID already exists', {}, 409);
    model.tasks.push(data.task);
  } else if (data.action === 'update') {
    if (data.task !== undefined || !data.patch || typeof data.patch !== 'object' || Array.isArray(data.patch) || !Object.keys(data.patch).length || Object.keys(data.patch).some(k => !taskFields.includes(k))) problem('task/argument', 'Update accepts only declared task fields');
    const task = model.tasks.find(t => t.id === data.taskId);
    if (!task) problem('task/missing', 'Task no longer exists', {}, 409);
    Object.assign(task, data.patch);
  } else if (data.action === 'delete') {
    if (data.task !== undefined || data.patch !== undefined || typeof data.taskId !== 'string') problem('task/argument', 'Delete requires only a taskId');
    if (!model.tasks.some(t => t.id === data.taskId)) problem('task/missing', 'Task no longer exists', {}, 409);
    model.tasks = model.tasks.filter(t => t.id !== data.taskId);
  } else if (data.action === 'restore') {
    if (data.task !== undefined || data.patch !== undefined || data.taskId !== undefined || typeof data.deletionId !== 'string') problem('task/argument', 'Restore requires only the deletion operation ID');
    const deletion = authority.records.find(r => r.operationId === data.deletionId && r.reason === 'task-delete');
    const prior = deletion && authority.records.find(r => r.cursor === deletion.cursor - 1);
    if (!prior) problem('task/missing', 'Deletion history is unavailable', {}, 409);
    const deleted = parseModel(authority.loadObject(prior.object).source).model.tasks?.find(t => t.id === deletion.taskId);
    if (!deleted) problem('task/missing', 'Deleted task is unavailable', {}, 409);
    if (model.tasks.some(t => t.id === deleted.id)) problem('task/conflict', 'Task ID already exists; restore will not overwrite it', {}, 409);
    taskId = deleted.id;model.tasks.push(deleted);
  } else problem('task/argument', 'Use create, update, delete or restore');
  const source = JSON.stringify(model, null, 2) + '\n';
  parseModel(source);
  const record = transact(source, 'task-' + data.action, { operationId: data.operationId, payloadHash, taskId });
  return { ok: true, cursor: record.cursor, taskId };
}
