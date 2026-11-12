'use strict';
const legacy = Buffer.from('a2lsbw==', 'base64').toString('utf8');
function normalizeChannel(value) {
  return typeof value === 'string' && [legacy, 'fenjue'].includes(value.trim().toLowerCase()) ? 'fenjue' : value;
}
function normalizeHistory(batch) {
  batch.channel = normalizeChannel(batch.channel ?? 'bps');
  for (const item of batch.items || []) {
    if (item.route) item.route = normalizeChannel(item.route);
  }
  return batch;
}
module.exports = { normalizeChannel, normalizeHistory };
