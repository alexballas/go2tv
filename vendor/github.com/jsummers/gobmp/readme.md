gobmp
=====

A Go package for reading and writing BMP image files.


Installation
------------

To download and install, at a command prompt type:

    go get github.com/jsummers/gobmp


Documentation
-------------

Gobmp is designed to work the same as Go's standard
[image modules](http://golang.org/pkg/image/). Importing it will automatically
cause the image.Decode function to support reading BMP files.

The documentation may be read online at
[GoDoc](http://godoc.org/github.com/jsummers/gobmp).

Or, after installing, type:

    godoc github.com/jsummers/gobmp | more


Status
------

The decoder supports almost all types of BMP images.

By default, the encoder will write a 24-bit RGB image, or a 1-, 4-, or 8-bit
paletted image. Support for 32-bit RGBA images can optionally be enabled.
Writing compressed images is not supported.


License
-------

Gobmp is distributed under an MIT-style license. Refer to the COPYING.txt
file.

Copyright &copy; 2012-2015 Jason Summers
<[jason1@pobox.com](mailto:jason1@pobox.com)>
