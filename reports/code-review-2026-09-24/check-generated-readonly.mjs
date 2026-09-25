import fs from 'node:fs';
import path from 'node:path';
import { generateClient, generateTypes, readOpenApi } from '../../scripts/lib/openapi-sdk.mjs';
import { buildRouteCoverage } from '../../scripts/update-api-route-coverage.mjs';

const root = process.cwd();
const openapiPath = path.join(root, 'services/api-gateway/openapi/edugrade-api.openapi.json');
const spec = readOpenApi(openapiPath);
const checks = [
  ['packages/sdk/src/generated/types.ts', generateTypes(spec)],
  ['packages/sdk/src/generated/client.ts', generateClient(spec)],
  ['services/api-gateway/openapi/route-coverage.json', JSON.stringify(buildRouteCoverage({
    root, openapiPath,
    gatewayRoot: path.join(root, 'services/api-gateway/internal'),
    exceptionsPath: path.join(root, 'contracts/openapi/registered-route-exceptions.json'),
  }), null, 2) + '\n'],
];
for (const [file, expected] of checks) {
  const equal = fs.readFileSync(path.join(root, file), 'utf8') === expected;
  console.log(`${equal ? 'MATCH' : 'STALE'} ${file}`);
  if (!equal) process.exitCode = 1;
}
