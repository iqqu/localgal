# Developing

Manual testing the basic things is simple enough and proper automated testing of everything is tedious enough and browser-dependent enough that I'm just manually testing most things.

Manual test checklist:
* Page performance seems acceptable and looks accurately reported in the footer
* `#top` scales image to fit between the prev/next rail and the bottom of viewport (when viewing file in a gallery)
* `#top` scales image to fit between the header and the bottom of viewport (when viewing file in a gallery)
* `#main-content` scrolls image or video to top of screen on load with autojump
* `#main-content` scrolls image or video to top of screen on clicking "Jump to Content"
* `#main-content` scales image or video to fit the full screen on load with autojump
* `#main-content` scales image or video to fit the full screen on clicking "Jump to Content"
* `#main-content` does not scale an image or video past 1:1
* Clicking an image scales an image to the full page width, but not past 1:1
* When an image's native resolution is wider than the page, clicking the image does not create a horizontal scrollbar when vertical scrollbars are non-overlay type
* When an image's native resolution is wider than the page, clicking the image does not crop any sides
* Autojump and manual jump scroll to the correct position every time on Chrome and Firefox
* Autojump and manual jump automatically focus a video to play with the space bar
* All the hotkeys work
* Prev/next rail crops images at the center to fill the thumbnail
* Gallery browse shows a grid of galleries
* Pagination links and number entry work
* Gallery view sizes columns responsively
* Gallery view images and videos are shown at their native aspect ratio
* No external network requests
* `#top`, `#main-content`, and image zoom all work the same with JS disabled, except for JS-specific things like focusing the video and hotkeys
* Page titles look as expected
* SQL query performance did not worsen; page loads are snappy with a 1 GiB database
* Javascript detection and related gallery lazy loading works correctly on file pages, both in and out of a gallery
* Whitespace in generated pages isn't excessive
