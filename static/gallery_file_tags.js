// Load galleries asynchronously
(function () {
    // /gallery/{host}/{gid}
    const pathParts = document.location.pathname.split('/');
    // ['', 'gallery', 'host', 'gid']
    pathParts.shift(); // Remove empty first part
    if (pathParts[pathParts.length - 1] === '') {
        // Remove empty last part (happens with trailing slash)
        pathParts.pop();
    }

    let host;
    let gid;
    if (pathParts[0] === 'gallery' && pathParts.length === 3) {
        host = pathParts[1];
        gid = pathParts[2];
    } else {
        return;
    }
    const asyncFileTagsEl = document.querySelector('#async-file-tags');
    if (asyncFileTagsEl == null) {
        return;
    }
    fetch(`/gallery-file-tags/${host}/${gid}`)
        .then(function (response) {
            if (response.ok) {
                return response.text();
            } else {
                throw new Error('Unable to load related galleries');
            }
        })
        .then(function (text) {
            asyncFileTagsEl.innerHTML = text;
        })
        .catch(function (error) {
            asyncFileTagsEl.innerHTML = error;
        });
})();
