// Load galleries asynchronously
(function () {
    // /file/{host}/{fileId}
    // /gallery/{host}/{gid}/{fileId}
    const pathParts = document.location.pathname.split('/');
    // ['', 'file', 'host', 'fileId']
    // ['', 'gallery', 'host', 'gid', 'fileId']
    pathParts.shift(); // Remove empty first part
    if (pathParts[pathParts.length - 1] === '') {
        // Remove empty last part (happens with trailing slash)
        pathParts.pop();
    }

    let host;
    let fileId;
    if (pathParts[0] === 'gallery' && pathParts.length === 4) {
        host = pathParts[1];
        fileId = pathParts[3];
    } else if (pathParts[0] === 'file' && pathParts.length === 3) {
        host = pathParts[1];
        fileId = pathParts[2];
    } else {
        return;
    }
    const asyncAlbumsEl = document.querySelector('#async-albums');
    if (asyncAlbumsEl == null) {
        return;
    }
    fetch(`/file/${host}/${fileId}/galleries`)
        .then(function (response) {
            if (response.ok) {
                return response.text();
            } else {
                throw new Error('Unable to load related galleries');
            }
        })
        .then(function (text) {
            asyncAlbumsEl.innerHTML = text;
        })
        .catch(function (error) {
            asyncAlbumsEl.innerHTML = error;
        });
})();
