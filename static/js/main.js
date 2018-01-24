
var otitle = document.title;
var xhr;

$(function() {
  $("A.delete").click(onDeleteClick);
  $('#upload').on('submit', onUploadSubmit);
  $('#cancel').on('click', onCancelClick);
  $(document).on('change', ':file', onFileChange);
  $(':file').on('fileselect', onFileSelect);

});

function onDeleteClick(e) {
    if(!confirm("Are you sure you wish to delete this file?")) {
        e.preventDefault();
    }
}

function onUploadSubmit(event) {
    event.preventDefault();
    var $f = $(this);
    var formData = new FormData($f[0]);
    $.ajax({
        xhr : uploadXHR,
        type : 'POST',
        url : $f.attr("action"),
        data : formData,
        processData : false,
        contentType : false,
        error: onUploadError,
        success : onUploadSuccess
    });
}

function uploadXHR() {
    xhr = new window.XMLHttpRequest();
    xhr.upload.addEventListener('progress', onUploadProgress);
    return xhr;
}

function onUploadProgress(e) {
    if (!e.lengthComputable) {
        return;
    }
    var percent = Math.round((e.loaded / e.total) * 100);
    document.title = percent + "% upload complete";
    setProgressBar(percent);
}

function onUploadError(jqXHR, textStatus, errorThrown) {
    document.title = otitle;
    setProgressBar(0);
    var msg = "An error occured during the upload. Please try again. " + errorThrown;
    alert(msg);
    xhr = null;
}

function onUploadSuccess() {
    xhr = null;
    document.location = "/";
}


function onCancelClick() {
    if(xhr.abort !== undefined) {
        xhr.abort();
    }
}

function setProgressBar(percent) {
    var $p = $('#progressbar');
    var width = percent + '%';
    $p.attr('aria-valuenow', percent).css('width', width).text(width);
}

 
function onFileChange() {
    var input = $(this),
        numFiles = input.get(0).files ? input.get(0).files.length : 1,
        label = input.val().replace(/\\/g, '/').replace(/.*\//, '');
    input.trigger('fileselect', [numFiles, label]);
}


function onFileSelect(event, numFiles, label) {
    var input = $(this).parents('.input-group').find(':text'),
        log = numFiles > 1 ? numFiles + ' files selected' : label;
    if(input.length) {
        input.val(log);
    } else {
        if(log) alert(log);
    }
}

