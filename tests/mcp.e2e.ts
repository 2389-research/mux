// ABOUTME: Runs real Go MCP and filesystem contracts through tester-army/e2e.
// ABOUTME: Requires explicit Go test passes so skipped or missing tests cannot pass.
import { contract } from './go-contract.ts';

for (const [flow, description] of [
  ['round_trip', 'discover, echo a large Unicode frame, and close the real MCP process'],
  ['error_recovery', 'recover from invalid calls on the same MCP connection'],
  ['approval', 'execute only the approved call through the MCP tool adapter'],
]) {
  contract(flow, description, '.', [`TestMCPProcessContract/${flow}`], `^TestMCPProcessContract$/${flow}$`);
}

contract('http_sdk', 'discover, notify, recover and close through the official SDK HTTP transport', '.', ['TestMCPHTTPProcessContract']);
contract('filesystem_reference', 'create, read and list files through the official filesystem MCP server', '.', ['TestMCPFilesystemProcessContract']);

contract('sessions', 'save, load, list, and delete sessions on the real filesystem', './session', [
  'TestFileStore_SaveLoadRoundTrip', 'TestFileStore_ListSorted', 'TestFileStore_DeleteIdempotent',
]);
contract('skills', 'load skill files and reject missing, duplicate, and malformed catalogs', './skill', [
  'TestLoadDir', 'TestLoadDirMissingDirErrors', 'TestLoadDirDuplicateNameErrors', 'TestLoadDirMalformedSkillErrors',
]);
