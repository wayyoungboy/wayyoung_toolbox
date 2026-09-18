import { TrimApp } from './vendor/trimjs-web-app.js';
import { createDirectoryAuthorizer, installToolboxDirectoryBridge } from './directory-authorization.js';

const authorize = createDirectoryAuthorizer(() => new TrimApp({ debug: false }));
installToolboxDirectoryBridge(window, authorize);
